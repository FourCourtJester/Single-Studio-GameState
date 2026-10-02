package adapter

import (
	"context"
	"log/slog"
	"time"

	"github.com/coder/websocket"
)

// WSClient connects out to a WebSocket the game serves and emits each
// message as-is. Rocket League's Stats API works this way. It reconnects
// whenever the game isn't running or closes the socket.
type WSClient struct {
	URL   string
	Log   *slog.Logger
	Retry time.Duration // wait between connection attempts
}

// Run connects and relays until ctx is cancelled.
func (c *WSClient) Run(ctx context.Context, emit func([]byte)) error {
	waiting := false
	for {
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		conn, _, err := websocket.Dial(dialCtx, c.URL, nil)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			if !waiting {
				c.Log.Info("source unavailable, waiting", "url", c.URL)
				waiting = true
			}
		} else {
			waiting = false
			c.Log.Info("source connected", "url", c.URL)
			conn.SetReadLimit(maxLiveAPIMessage)
			err := read(ctx, conn, emit)
			conn.CloseNow()
			if ctx.Err() != nil {
				return nil
			}
			c.Log.Info("source disconnected", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(c.Retry):
		}
	}
}

func read(ctx context.Context, conn *websocket.Conn, emit func([]byte)) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		emit(data)
	}
}
