package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const maxGSIBody = 4 << 20

// Receiver is an HTTP server the game POSTs to. CS2 and Dota 2 Game State
// Integration both work this way. The user's own GSI config file points the
// game at it; writing that file is outside this app.
type Receiver struct {
	Addr string
	Log  *slog.Logger
}

// Run serves until ctx is cancelled.
func (r *Receiver) Run(ctx context.Context, emit func([]byte)) error {
	ln, err := Listen(r.Addr)
	if err != nil {
		return err
	}
	r.Log.Info("waiting for game state integration", "addr", ln.Addr().String())
	return serve(ctx, ln, r.Handler(emit))
}

// Handler returns the HTTP handler that validates and emits each POST.
func (r *Receiver) Handler(emit func([]byte)) http.Handler {
	var seen atomic.Bool
	// The game posts many times a second, so a rejection is logged only when
	// its reason changes, not on every post.
	var lastReject atomic.Pointer[string]
	reject := func(w http.ResponseWriter, reason string, code int) {
		if prev := lastReject.Swap(&reason); prev == nil || *prev != reason {
			r.Log.Warn("rejected game state: " + reason)
		}
		http.Error(w, reason, code)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(req.Body, maxGSIBody+1))
		if err != nil {
			reject(w, "read failed", http.StatusBadRequest)
			return
		}
		if len(body) > maxGSIBody {
			reject(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		if !json.Valid(body) {
			reject(w, "payload is not JSON", http.StatusBadRequest)
			return
		}
		lastReject.Store(nil)
		if !seen.Swap(true) {
			r.Log.Info("source connected")
		}
		emit(body)
		w.WriteHeader(http.StatusOK)
	})
}

// Listen opens a TCP listener on addr, explaining the common failure in
// plain words. On 127.0.0.1 it also listens on IPv6's loopback, ::1, where it
// can: Windows resolves "localhost" to ::1 first, and not every client tries
// 127.0.0.1 after that is refused.
func Listen(addr string) (net.Listener, error) {
	ln, err := listen(addr)
	if err != nil {
		return nil, err
	}
	if host, _, _ := net.SplitHostPort(addr); host != "127.0.0.1" {
		return ln, nil
	}
	port := ln.Addr().(*net.TCPAddr).Port
	v6, err := net.Listen("tcp", net.JoinHostPort("::1", strconv.Itoa(port)))
	if err != nil {
		return ln, nil // no IPv6, or ::1 taken: 127.0.0.1 still works
	}
	return newMultiListener(ln, v6), nil
}

func listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	msg := err.Error()
	if errors.Is(err, syscall.EADDRINUSE) ||
		strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "Only one usage of each socket address") { // Windows
		_, port, _ := net.SplitHostPort(addr)
		return nil, fmt.Errorf("port %s is already in use by another program", port)
	}
	return nil, fmt.Errorf("can't listen on %s: %w", addr, err)
}

// multiListener accepts from several listeners as one.
type multiListener struct {
	lns   []net.Listener
	conns chan accepted
	done  chan struct{}
	once  sync.Once
}

type accepted struct {
	conn net.Conn
	err  error
}

func newMultiListener(lns ...net.Listener) *multiListener {
	m := &multiListener{lns: lns, conns: make(chan accepted), done: make(chan struct{})}
	for _, ln := range lns {
		go func() {
			for {
				c, err := ln.Accept()
				select {
				case m.conns <- accepted{c, err}:
				case <-m.done:
					if c != nil {
						c.Close()
					}
					return
				}
				if err != nil {
					return
				}
			}
		}()
	}
	return m
}

func (m *multiListener) Accept() (net.Conn, error) {
	select {
	case a := <-m.conns:
		return a.conn, a.err
	case <-m.done:
		return nil, net.ErrClosed
	}
}

func (m *multiListener) Close() error {
	m.once.Do(func() {
		close(m.done)
		for _, ln := range m.lns {
			ln.Close()
		}
	})
	return nil
}

// Addr is the first listener's address, 127.0.0.1's.
func (m *multiListener) Addr() net.Addr { return m.lns[0].Addr() }

// serve runs an HTTP server on ln until ctx is cancelled.
func serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
