package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

const maxGSIBody = 4 << 20

// Receiver is an HTTP server the game POSTs to. CS2 and Dota 2 Game State
// Integration both work this way; see the gsi package for the config file
// that points the game at it.
type Receiver struct {
	Addr  string
	Token string // when set, payloads must carry this auth token
	Log   *slog.Logger
}

// Run serves until ctx is cancelled.
func (r *Receiver) Run(ctx context.Context, emit func([]byte)) error {
	ln, err := net.Listen("tcp", r.Addr)
	if err != nil {
		return err
	}
	r.Log.Info("waiting for game state integration", "addr", ln.Addr().String())
	return serve(ctx, ln, r.Handler(emit))
}

// Handler returns the HTTP handler that validates and emits each POST.
func (r *Receiver) Handler(emit func([]byte)) http.Handler {
	var seen atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(req.Body, maxGSIBody+1))
		if err != nil {
			http.Error(w, "read failed", http.StatusBadRequest)
			return
		}
		if len(body) > maxGSIBody {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		if !json.Valid(body) {
			http.Error(w, "payload is not JSON", http.StatusBadRequest)
			return
		}
		body, ok := checkAuth(body, r.Token)
		if !ok {
			http.Error(w, "bad auth token", http.StatusUnauthorized)
			return
		}
		if !seen.Swap(true) {
			r.Log.Info("source connected")
		}
		emit(body)
		w.WriteHeader(http.StatusOK)
	})
}

// checkAuth verifies the GSI auth block when a token is configured and
// strips it from the payload, so the token is never relayed to browsers.
func checkAuth(body []byte, token string) ([]byte, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		// Valid JSON but not an object: nothing to strip.
		return body, token == ""
	}
	raw, present := fields["auth"]
	if token != "" {
		var auth struct {
			Token string `json:"token"`
		}
		if !present || json.Unmarshal(raw, &auth) != nil || auth.Token != token {
			return nil, false
		}
	}
	if !present {
		return body, true
	}
	delete(fields, "auth")
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, false
	}
	return out, true
}

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
