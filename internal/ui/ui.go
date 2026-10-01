// Package ui serves the companion's one-page control panel: an on/off
// switch, a game picker and an error pane.
package ui

import (
	_ "embed"
	"encoding/json"
	"net"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/fourcourtjester/single-studio-gamestate/internal/adapter"
	"github.com/fourcourtjester/single-studio-gamestate/internal/control"
	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
)

//go:embed index.html
var page []byte

// Server is the control panel and its JSON API.
type Server struct {
	Ctrl   *control.Controller
	Errors *control.ErrorLog
	Hub    *relay.Hub
	Bind   string // the address the companion listens on
}

// Register adds the panel's routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	})
	mux.Handle("GET /api/state", s.guard(s.state))
	mux.Handle("POST /api/game", s.guard(s.selectGame))
	mux.Handle("POST /api/start", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if err := s.Ctrl.Start(); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.Handle("POST /api/stop", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.Ctrl.Stop()
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.Handle("POST /api/errors/clear", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.Errors.Clear()
		w.WriteHeader(http.StatusNoContent)
	}))
}

type stateResponse struct {
	control.State
	Clients int             `json:"clients"`
	Relay   string          `json:"relay"`
	Games   []adapter.Title `json:"games"`
	Errors  []control.Entry `json:"errors"`
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(stateResponse{
		State:   s.Ctrl.State(),
		Clients: s.Hub.Stats().Clients,
		Relay:   "ws://" + r.Host + "/ws",
		Games:   adapter.Titles,
		Errors:  s.Errors.Entries(),
	})
}

func (s *Server) selectGame(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Game string `json:"game"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.Ctrl.Select(body.Game); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// guard stops other websites from driving the panel. The API only answers
// requests addressed to this machine (which defeats DNS rebinding) and,
// when the browser says where a request came from, only the panel itself.
func (s *Server) guard(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.localHost(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	})
}

func (s *Server) localHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.Equal(net.ParseIP(s.Bind)))
}

// Open opens url in the user's default browser.
func Open(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
