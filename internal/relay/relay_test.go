package relay

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestEncodeJSON(t *testing.T) {
	got := Encode("sc2", []byte(`{"isReplay":false}`), time.UnixMilli(42))
	want := `{"ns":"sc2","ts":42,"data":{"isReplay":false}}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestEncodeBinary(t *testing.T) {
	got := Encode("apex", []byte{0x0a, 0x01, 0xff}, time.UnixMilli(1))
	want := `{"ns":"apex","ts":1,"encoding":"base64","data":"CgH/"}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func dial(t *testing.T, srv *httptest.Server) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn, ctx
}

func read(t *testing.T, ctx context.Context, conn *websocket.Conn) Envelope {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func waitForClients(t *testing.T, h *Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.Stats().Clients != n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d clients", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHubPublishesToClients(t *testing.T) {
	hub := NewHub([]string{"*"}, nil)
	srv := httptest.NewServer(hub)
	defer srv.Close()

	a, ctx := dial(t, srv)
	b, _ := dial(t, srv)
	waitForClients(t, hub, 2)

	hub.Publish("cs2", []byte(`{"round":3}`))
	for _, c := range []*websocket.Conn{a, b} {
		env := read(t, ctx, c)
		if env.NS != "cs2" || string(env.Data) != `{"round":3}` {
			t.Fatalf("unexpected envelope %+v", env)
		}
	}
}

func TestHubReplaysLatestOnConnect(t *testing.T) {
	hub := NewHub([]string{"*"}, nil)
	srv := httptest.NewServer(hub)
	defer srv.Close()

	hub.Publish("sc2", []byte(`{"tick":1}`))
	hub.Publish("sc2", []byte(`{"tick":2}`))

	conn, ctx := dial(t, srv)
	env := read(t, ctx, conn)
	if string(env.Data) != `{"tick":2}` {
		t.Fatalf("replayed %s, want the latest payload", env.Data)
	}
}

func TestHubRejectsDisallowedOrigin(t *testing.T) {
	hub := NewHub([]string{"studio.example"}, nil)
	srv := httptest.NewServer(hub)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	opts := &websocket.DialOptions{HTTPHeader: map[string][]string{"Origin": {"https://evil.example"}}}
	if conn, _, err := websocket.Dial(ctx, url, opts); err == nil {
		conn.CloseNow()
		t.Fatal("expected dial from a disallowed origin to fail")
	}
}

func TestDisconnectAll(t *testing.T) {
	hub := NewHub([]string{"*"}, nil)
	srv := httptest.NewServer(hub)
	defer srv.Close()

	conn, ctx := dial(t, srv)
	waitForClients(t, hub, 1)
	hub.DisconnectAll()

	_, _, err := conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("got %v, want a going-away close", err)
	}
	waitForClients(t, hub, 0)
}
