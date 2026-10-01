// Package relay passes game messages through to Single Studio over one local
// WebSocket. Every message goes out exactly as the game sent it: nothing is
// wrapped, added or changed. Yjs on the Single Studio side turns repeated
// identical payloads into no-ops.
package relay

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	sendBuffer   = 64
	writeTimeout = 5 * time.Second
)

type client struct {
	send chan []byte
	// why the hub dropped the client, set before send is closed
	code   websocket.StatusCode
	reason string
}

// Hub holds the connected clients and the latest message, so a client that
// connects (or reconnects) mid-session gets the current state immediately
// instead of waiting for the next one.
type Hub struct {
	origins []string
	log     *slog.Logger

	mu          sync.Mutex
	clients     map[*client]struct{}
	last        []byte
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
	}
}

// Publish sends a game message to every connected client, unchanged.
func (h *Hub) Publish(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = msg
	h.lastPublish = time.Now()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			// A client this far behind is stuck; drop it rather than block
			// the adapter. It can reconnect and get the latest state.
			h.log.Warn("dropping slow client")
			h.drop(c, websocket.StatusPolicyViolation, "client too slow")
		}
	}
}

// Forget drops the latest message, so clients that connect after a switch
// to another game aren't sent the previous game's state.
func (h *Hub) Forget() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = nil
}

// DisconnectAll closes every client connection, for when the relay moves
// to another port. The latest message is kept; clients get it again when
// they reconnect on the new port.
func (h *Hub) DisconnectAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		h.drop(c, websocket.StatusGoingAway, "relay moved to another port")
	}
}

// drop removes a client and closes its queue; h.mu must be held.
func (h *Hub) drop(c *client, code websocket.StatusCode, reason string) {
	c.code, c.reason = code, reason
	delete(h.clients, c)
	close(c.send)
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

// ServeHTTP upgrades the request to a WebSocket and streams game messages to
// it until the client goes away. Clients are receive-only.
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
	if h.last != nil {
		c.send <- h.last
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
				conn.Close(c.code, c.reason)
				return
			}
			if err := write(ctx, conn, msg); err != nil {
				return
			}
		}
	}
}

// write sends msg as the game sent it: text such as JSON as a text frame,
// anything else (Apex LiveAPI in protobuf mode) as a binary frame.
func write(ctx context.Context, conn *websocket.Conn, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	typ := websocket.MessageText
	if !utf8.Valid(msg) {
		typ = websocket.MessageBinary
	}
	return conn.Write(ctx, typ, msg)
}
