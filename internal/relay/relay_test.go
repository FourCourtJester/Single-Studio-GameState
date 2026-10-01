package relay

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

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

func read(t *testing.T, ctx context.Context, conn *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return typ, data
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

func TestHubPassesMessagesThroughUnchanged(t *testing.T) {
	hub := NewHub([]string{"*"}, nil)
	srv := httptest.NewServer(hub)
	defer srv.Close()

	a, ctx := dial(t, srv)
	b, _ := dial(t, srv)
	waitForClients(t, hub, 2)

	cases := []struct {
		msg []byte
		typ websocket.MessageType
	}{
		{[]byte(`{"Event":"UpdateState","Data":"{\"MatchGuid\":\"a\"}"}`), websocket.MessageText},
		{[]byte{0x0a, 0x01, 0xff}, websocket.MessageBinary}, // protobuf from Apex
	}
	for _, c := range cases {
		hub.Publish(c.msg)
		for _, conn := range []*websocket.Conn{a, b} {
			typ, got := read(t, ctx, conn)
			if typ != c.typ || !bytes.Equal(got, c.msg) {
				t.Fatalf("got %v %q, want %v %q", typ, got, c.typ, c.msg)
			}
		}
	}
}

func TestHubReplaysLatestOnConnect(t *testing.T) {
	hub := NewHub([]string{"*"}, nil)
	srv := httptest.NewServer(hub)
	defer srv.Close()

	hub.Publish([]byte(`{"tick":1}`))
	hub.Publish([]byte(`{"tick":2}`))

	conn, ctx := dial(t, srv)
	if _, got := read(t, ctx, conn); string(got) != `{"tick":2}` {
		t.Fatalf("replayed %s, want the latest message", got)
	}
}

func TestHubForgetsOnGameSwitch(t *testing.T) {
	hub := NewHub([]string{"*"}, nil)
	srv := httptest.NewServer(hub)
	defer srv.Close()

	hub.Publish([]byte(`{"old":"game"}`))
	hub.Forget()
	conn, ctx := dial(t, srv)
	waitForClients(t, hub, 1)
	hub.Publish([]byte(`{"new":"game"}`))
	if _, got := read(t, ctx, conn); string(got) != `{"new":"game"}` {
		t.Fatalf("got %s first; the previous game's message should be forgotten", got)
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
