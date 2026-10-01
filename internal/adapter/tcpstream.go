package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"time"
)

// TCPStream connects out to a TCP socket the game serves and emits each JSON
// object in its stream. Rocket League's Stats API works this way: it writes
// JSON objects back to back with no delimiter, which json.Decoder splits
// natively. Each object is emitted as-is.
type TCPStream struct {
	Addr  string
	Log   *slog.Logger
	Retry time.Duration // wait between connection attempts
}

// Run connects, reconnecting whenever the game isn't running or closes the
// socket, until ctx is cancelled.
func (s *TCPStream) Run(ctx context.Context, emit func([]byte)) error {
	var dialer net.Dialer
	waiting := false
	for {
		conn, err := dialer.DialContext(ctx, "tcp", s.Addr)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			if !waiting {
				s.Log.Info("source unavailable, waiting", "addr", s.Addr)
				waiting = true
			}
		} else {
			waiting = false
			s.Log.Info("source connected", "addr", s.Addr)
			err := s.read(ctx, conn, emit)
			if ctx.Err() != nil {
				return nil
			}
			s.Log.Info("source disconnected", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(s.Retry):
		}
	}
}

func (s *TCPStream) read(ctx context.Context, conn net.Conn, emit func([]byte)) error {
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()

	dec := json.NewDecoder(conn)
	for {
		var msg json.RawMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("the game closed the connection")
			}
			return err
		}
		emit(msg)
	}
}
