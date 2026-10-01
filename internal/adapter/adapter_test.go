package adapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestPollerCombinesEndpoints(t *testing.T) {
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

	got := c.wait(t)
	want := `{"game":{"isReplay":false,"displayTime":12.5},"ui":{"activeScreens":[]}}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
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
