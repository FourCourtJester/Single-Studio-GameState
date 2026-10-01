// Package relay fans raw game payloads out to browser clients over one local
// WebSocket. It does no shaping or diffing: every payload is wrapped in a
// namespaced envelope and sent as-is. Yjs on the Single Studio side turns
// repeated identical payloads into no-ops.
package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Envelope is the one message shape every title is delivered in.
//
// Data holds the payload verbatim when it is JSON. Anything else (for example
// Apex LiveAPI in protobuf mode) is base64-encoded and Encoding is "base64".
type Envelope struct {
	NS       string          `json:"ns"`
	TS       int64           `json:"ts"`
	Encoding string          `json:"encoding,omitempty"`
	Data     json.RawMessage `json:"data"`
}

// Encode wraps a raw payload in an Envelope and marshals it.
func Encode(ns string, data []byte, now time.Time) []byte {
	env := Envelope{NS: ns, TS: now.UnixMilli()}
	if json.Valid(data) {
		env.Data = data
	} else {
		env.Encoding = "base64"
		env.Data, _ = json.Marshal(base64.StdEncoding.EncodeToString(data))
	}
	out, _ := json.Marshal(env)
	return out
}

const (
	sendBuffer   = 64
	writeTimeout = 5 * time.Second
)

type client struct {
	send chan []byte
}

// Hub holds the connected browser clients and the latest payload per
// namespace, so a client that connects (or reconnects) mid-session gets the
// current state immediately instead of waiting for the next tick.
type Hub struct {
	origins []string
	log     *slog.Logger

	mu          sync.Mutex
	clients     map[*client]struct{}
	last        map[string][]byte
	lastPublish time.Time
}

// NewHub returns a Hub that accepts WebSocket connections from the given
// origin patterns (see websocket.AcceptOptions.OriginPatterns).
func NewHub(origins []string, log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		origins: origins,
		log:     log,
		clients: make(map[*client]struct{}),
		last:    make(map[string][]byte),
	}
}

// Publish sends a raw payload to every connected client under namespace ns.
func (h *Hub) Publish(ns string, data []byte) {
	now := time.Now()
	msg := Encode(ns, data, now)

	h.mu.Lock()
	defer h.mu.Unlock()
	h.last[ns] = msg
	h.lastPublish = now
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			// A client this far behind is stuck; drop it rather than block
			// the adapter. It can reconnect and get the latest state.
			h.log.Warn("dropping slow client")
			delete(h.clients, c)
			close(c.send)
		}
	}
}

// Stats is a snapshot of the hub's state for the status endpoint.
type Stats struct {
	Clients     int       `json:"clients"`
	LastPublish time.Time `json:"lastPublish"`
}

// Stats returns the current client count and last publish time.
func (h *Hub) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Stats{Clients: len(h.clients), LastPublish: h.lastPublish}
}

// ServeHTTP upgrades the request to a WebSocket and streams envelopes to it
// until the client goes away. Clients are receive-only.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.origins})
	if err != nil {
		h.log.Warn("websocket accept failed", "err", err, "origin", r.Header.Get("Origin"))
		return
	}
	defer conn.CloseNow()

	// Clients never send; CloseRead handles control frames and cancels ctx
	// when the client disconnects.
	ctx := conn.CloseRead(r.Context())

	c := &client{send: make(chan []byte, sendBuffer)}
	h.mu.Lock()
	for _, msg := range h.last {
		c.send <- msg
	}
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	h.log.Info("client connected", "remote", r.RemoteAddr)

	defer func() {
		h.mu.Lock()
		if _, ok := h.clients[c]; ok {
			delete(h.clients, c)
			close(c.send)
		}
		h.mu.Unlock()
		h.log.Info("client disconnected", "remote", r.RemoteAddr)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.send:
			if !ok {
				conn.Close(websocket.StatusPolicyViolation, "client too slow")
				return
			}
			if err := write(ctx, conn, msg); err != nil {
				return
			}
		}
	}
}

func write(ctx context.Context, conn *websocket.Conn, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, msg)
}
