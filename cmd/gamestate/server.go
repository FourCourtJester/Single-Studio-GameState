package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/fourcourtjester/single-studio-gamestate/internal/adapter"
	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
)

// relayServer serves the broadcast port Single Studio connects to, and can
// move to another port while running.
type relayServer struct {
	bind    string
	handler http.Handler
	hub     *relay.Hub
	log     *slog.Logger
	errc    chan error // reports a server that stopped by itself

	mu   sync.Mutex
	srv  *http.Server
	port int
}

func newRelayServer(bind string, handler http.Handler, hub *relay.Hub, log *slog.Logger) *relayServer {
	return &relayServer{bind: bind, handler: handler, hub: hub, log: log, errc: make(chan error, 1)}
}

// Listen moves the server to port. The new port is opened before the old
// one is closed, so a busy port leaves the server where it was. Clients on
// the old port are disconnected; Single Studio has to be pointed at the new
// one anyway.
func (r *relayServer) Listen(port int) error {
	addr := net.JoinHostPort(r.bind, strconv.Itoa(port))
	ln, err := adapter.Listen(addr)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.srv != nil {
		shutdown(r.srv)
		r.hub.DisconnectAll()
	}
	srv := &http.Server{Handler: r.handler, ReadHeaderTimeout: 5 * time.Second}
	r.srv, r.port = srv, port
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			select {
			case r.errc <- err:
			default:
			}
		}
	}()
	r.log.Info("relay listening", "ws", "ws://"+addr)
	return nil
}

// Port returns the port being served, or 0 if none.
func (r *relayServer) Port() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.port
}

// Close stops serving.
func (r *relayServer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.srv != nil {
		shutdown(r.srv)
		r.srv, r.port = nil, 0
	}
}

func shutdown(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
