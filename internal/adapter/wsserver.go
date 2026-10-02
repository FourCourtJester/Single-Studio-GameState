package adapter

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
)

const maxLiveAPIMessage = 4 << 20

// WSServer is a WebSocket server the game connects out to as a client.
// Apex LiveAPI works this way: launch the game with
// +cl_liveapi_ws_servers "ws://127.0.0.1:<port>". Each message the game
// sends is emitted as-is, JSON or protobuf.
type WSServer struct {
	Addr string
	Log  *slog.Logger
}

// Run serves until ctx is cancelled.
func (s *WSServer) Run(ctx context.Context, emit func([]byte)) error {
	ln, err := Listen(s.Addr)
	if err != nil {
		return err
	}
	s.Log.Info("waiting for game to connect", "addr", "ws://"+ln.Addr().String())
	return serve(ctx, ln, s.Handler(emit))
}

// Handler returns the HTTP handler that accepts game connections.
func (s *WSServer) Handler(emit func([]byte)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The game is not a browser and sends no Origin header; there is no
		// cross-site risk to check for on this socket.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			s.Log.Warn("game websocket accept failed", "err", err)
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(maxLiveAPIMessage)

		s.Log.Info("source connected", "remote", r.RemoteAddr)
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				s.Log.Info("source disconnected", "remote", r.RemoteAddr, "err", err)
				return
			}
			emit(data)
		}
	})
}
