package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
)

func newTestRelay(t *testing.T) *relayServer {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := relay.NewHub([]string{"*"}, log)
	mux := http.NewServeMux()
	mux.Handle("GET /{$}", hub)
	r := newRelayServer("127.0.0.1", mux, hub, log)
	t.Cleanup(r.Close)
	return r
}

func port(t *testing.T) int {
	t.Helper()
	p, _ := strconv.Atoi(freePort(t))
	return p
}

func dialRelay(t *testing.T, port int) (*websocket.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+strconv.Itoa(port), nil)
	return conn, err
}

func TestRelayMovesPort(t *testing.T) {
	r := newTestRelay(t)
	a, b := port(t), port(t)
	if err := r.Listen(a); err != nil {
		t.Fatal(err)
	}
	old, err := dialRelay(t, a)
	if err != nil {
		t.Fatal(err)
	}
	defer old.CloseNow()

	if err := r.Listen(b); err != nil {
		t.Fatal(err)
	}
	if r.Port() != b {
		t.Fatalf("port = %d, want %d", r.Port(), b)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := old.Read(ctx); websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("old client: got %v, want a going-away close", err)
	}
	if conn, err := dialRelay(t, a); err == nil {
		conn.CloseNow()
		t.Fatal("old port still accepting connections")
	}
	conn, err := dialRelay(t, b)
	if err != nil {
		t.Fatalf("new port: %v", err)
	}
	conn.CloseNow()
}

func TestRelayStaysPutWhenNewPortIsBusy(t *testing.T) {
	r := newTestRelay(t)
	a := port(t)
	if err := r.Listen(a); err != nil {
		t.Fatal(err)
	}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	if err := r.Listen(busy.Addr().(*net.TCPAddr).Port); err == nil {
		t.Fatal("expected moving to a busy port to fail")
	}
	if r.Port() != a {
		t.Fatalf("port = %d, want it to stay %d", r.Port(), a)
	}
	conn, err := dialRelay(t, a)
	if err != nil {
		t.Fatalf("original port stopped working: %v", err)
	}
	conn.CloseNow()
}
