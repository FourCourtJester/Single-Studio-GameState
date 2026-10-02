package adapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// collector records emitted payloads.
type collector struct {
	mu   sync.Mutex
	got  [][]byte
	more chan struct{}
}

func newCollector() *collector { return &collector{more: make(chan struct{}, 100)} }

func (c *collector) emit(data []byte) {
	c.mu.Lock()
	c.got = append(c.got, bytes.Clone(data))
	c.mu.Unlock()
	c.more <- struct{}{}
}

func (c *collector) wait(t *testing.T) []byte {
	t.Helper()
	select {
	case <-c.more:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a payload")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.got[len(c.got)-1]
}

func TestNewCoversEveryGame(t *testing.T) {
	for _, title := range Titles {
		a, err := New(title.ID, Options{Bind: "127.0.0.1", Interval: time.Second, Log: quiet})
		if !title.Available {
			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("%s: got %v, want ErrNotImplemented", title.ID, err)
			}
			continue
		}
		if err != nil || a == nil {
			t.Errorf("%s: got %v, %v", title.ID, a, err)
		}
	}
	if _, err := New("valorant", Options{}); err == nil {
		t.Error("expected unknown game to fail")
	}
}

func TestPollerEmitsEachEndpointTagged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/game":
			io.WriteString(w, `{"isReplay":false,"displayTime":12.5}`)
		case "/ui":
			io.WriteString(w, `{"activeScreens":[]}`)
		}
	}))
	defer srv.Close()

	p := &Poller{
		Client:    srv.Client(),
		Interval:  time.Hour,
		Endpoints: []Endpoint{{Name: "game", URL: srv.URL + "/game"}, {Name: "ui", URL: srv.URL + "/ui"}},
		Log:       quiet,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	go p.Run(ctx, c.emit)

	c.wait(t)
	c.wait(t)
	c.mu.Lock()
	defer c.mu.Unlock()
	want := []string{
		`{"_ssg":"game","isReplay":false,"displayTime":12.5}`,
		`{"_ssg":"ui","activeScreens":[]}`,
	}
	for i, w := range want {
		if string(c.got[i]) != w {
			t.Errorf("message %d: got %s, want %s", i, c.got[i], w)
		}
	}
}

func TestSingleEndpointIsUntouched(t *testing.T) {
	body := `{ "activePlayer": {"level": 3} }`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	p := &Poller{Client: srv.Client(), Interval: time.Hour, Endpoints: []Endpoint{{Name: "all", URL: srv.URL}}, Log: quiet}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	go p.Run(ctx, c.emit)
	if got := c.wait(t); string(got) != body {
		t.Fatalf("got %s, want it byte for byte: %s", got, body)
	}
}

func TestTag(t *testing.T) {
	cases := map[string]string{
		`{"a":1}`:                 `{"_ssg":"ui","a":1}`,
		`{}`:                      `{"_ssg":"ui"}`,
		"  {\n  \"a\": [1, 2]\n}": "{\"_ssg\":\"ui\",\"a\": [1, 2]\n}", // the game's own spacing is kept
		`[1,2]`:                   `[1,2]`,                             // not an object: left alone
		`not json`:                `not json`,
	}
	for in, want := range cases {
		if got := string(tag([]byte(in), "ui")); got != want {
			t.Errorf("tag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPollerSkipsFailedPolls(t *testing.T) {
	var mu sync.Mutex
	up := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !up {
			http.Error(w, "no game", http.StatusServiceUnavailable)
			up = true
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	p := &Poller{Client: srv.Client(), Interval: 10 * time.Millisecond, Endpoints: []Endpoint{{URL: srv.URL}}, Log: quiet}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	done := make(chan error)
	go func() { done <- p.Run(ctx, c.emit) }()

	if got := c.wait(t); string(got) != `{"ok":true}` {
		t.Fatalf("got %s", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v after cancel, want nil", err)
	}
}

func TestRiotClientOnlyDialsLeague(t *testing.T) {
	_, err := newRiotClient().Get("https://127.0.0.1:443/")
	if err == nil || !strings.Contains(err.Error(), "may only dial") {
		t.Fatalf("got %v, want the dial restriction to refuse", err)
	}
}

func post(h http.Handler, method, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, "/", strings.NewReader(body)))
	return rec
}

func TestReceiver(t *testing.T) {
	r := &Receiver{Log: quiet}
	c := newCollector()
	h := r.Handler(c.emit)

	if rec := post(h, http.MethodPost, `{"map":{"name":"de_ancient"}}`); rec.Code != http.StatusOK {
		t.Fatalf("valid payload: %d", rec.Code)
	}
	if got := c.wait(t); string(got) != `{"map":{"name":"de_ancient"}}` {
		t.Fatalf("got %s", got)
	}
	if rec := post(h, http.MethodGet, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
	if rec := post(h, http.MethodPost, "not json"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad JSON: %d", rec.Code)
	}
}

func TestReceiverPassesPayloadThroughUntouched(t *testing.T) {
	c := newCollector()
	h := (&Receiver{Log: quiet}).Handler(c.emit)
	body := `{"auth":{"token":"s3cret"},"round":{}}`
	if rec := post(h, http.MethodPost, body); rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	if got := c.wait(t); string(got) != body {
		t.Fatalf("payload changed: %s", got)
	}
}

func TestWSServerRelaysGameMessages(t *testing.T) {
	s := &WSServer{Log: quiet}
	c := newCollector()
	srv := httptest.NewServer(s.Handler(c.emit))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageText, []byte(`{"category":"matchStateEnd"}`))
	if got := c.wait(t); string(got) != `{"category":"matchStateEnd"}` {
		t.Fatalf("text: got %s", got)
	}
	conn.Write(ctx, websocket.MessageBinary, []byte{0x0a, 0x01})
	if got := c.wait(t); !bytes.Equal(got, []byte{0x0a, 0x01}) {
		t.Fatalf("binary: got %x", got)
	}
}

func TestWSClientRelaysAndReconnects(t *testing.T) {
	// A fake game: each connection gets the queued messages, then is closed.
	sessions := make(chan []string, 2)
	sessions <- []string{`{"Event":"UpdateState","Data":"{\"MatchGuid\":\"a\"}"}`, `{"Event":"BallHit","Data":"{}"}`}
	sessions <- []string{`{"Event":"MatchCreated","Data":"{}"}`}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for _, m := range <-sessions {
			conn.Write(r.Context(), websocket.MessageText, []byte(m))
		}
		conn.Close(websocket.StatusNormalClosure, "match over")
	}))
	defer srv.Close()

	cl := &WSClient{URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Log: quiet, Retry: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	done := make(chan error)
	go func() { done <- cl.Run(ctx, c.emit) }()

	want := []string{
		`{"Event":"UpdateState","Data":"{\"MatchGuid\":\"a\"}"}`,
		`{"Event":"BallHit","Data":"{}"}`,
		`{"Event":"MatchCreated","Data":"{}"}`, // after reconnecting
	}
	for range want {
		c.wait(t)
	}
	c.mu.Lock()
	for i, w := range want {
		if string(c.got[i]) != w {
			t.Errorf("message %d: got %s, want %s", i, c.got[i], w)
		}
	}
	c.mu.Unlock()

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v after cancel, want nil", err)
	}
}

func TestPortOverride(t *testing.T) {
	a, err := New(CS2, Options{Bind: "127.0.0.1", Port: 50000, Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.(*Receiver).Addr; got != "127.0.0.1:50000" {
		t.Errorf("cs2 override: %s", got)
	}
	a, _ = New(RL, Options{Log: quiet})
	if got := a.(*WSClient).URL; got != "ws://localhost:49124" {
		t.Errorf("rl default: %s", got)
	}
	a, _ = New(SC2, Options{Port: 7000, Interval: time.Second, Log: quiet})
	if got := a.(*Poller).Endpoints[0].URL; got != "http://localhost:7000/game" {
		t.Errorf("sc2 override: %s", got)
	}
}

func TestListenAnswersOnBothLoopbacks(t *testing.T) {
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, ok := ln.(*multiListener); !ok {
		t.Skip("no IPv6 loopback here")
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	for _, host := range []string{"127.0.0.1", "::1", "localhost"} {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 2*time.Second)
		if err != nil {
			t.Errorf("%s: %v", host, err)
			continue
		}
		c.Close()
	}
	ln.Close()
	if _, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Accept after Close: %v", err)
	}
}

func TestMultiListenerAcceptsFromEach(t *testing.T) {
	var lns []net.Listener
	for range 2 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		lns = append(lns, ln)
	}
	m := newMultiListener(lns...)
	defer m.Close()
	for _, ln := range lns {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		got, err := m.Accept()
		if err != nil {
			t.Fatal(err)
		}
		if got.LocalAddr().String() != ln.Addr().String() {
			t.Errorf("accepted on %s, want %s", got.LocalAddr(), ln.Addr())
		}
		got.Close()
	}
	if m.Addr() != lns[0].Addr() {
		t.Errorf("Addr = %s, want the first listener's", m.Addr())
	}
	m.Close()
	if _, err := m.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Accept after Close: %v", err)
	}
	if _, err := net.Dial("tcp", lns[1].Addr().String()); err == nil {
		t.Error("Close should close every listener")
	}
}
