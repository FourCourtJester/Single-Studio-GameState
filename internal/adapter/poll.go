package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const (
	requestTimeout = 2 * time.Second
	maxPollBody    = 8 << 20
)

// lolURL is League's live client data endpoint. It is deliberately not
// configurable: the Riot client below skips certificate verification, and
// that must only ever apply to this one local address.
const (
	lolHost = "127.0.0.1:2999"
	lolURL  = "https://" + lolHost + "/liveclientdata/allgamedata"
)

// TagKey is the one field GameState adds to a game's messages, and only for
// games read over more than one address: it names the address a message
// came from (StarCraft II's "game" or "ui"), so Single Studio can tell the
// updates apart whatever their shape.
const TagKey = "_ssg"

// Endpoint is one URL a Poller fetches each tick.
type Endpoint struct {
	Name string // used as the TagKey value when there are several endpoints
	URL  string
}

// Poller fetches its endpoints on a timer and emits each response as its
// own message, unchanged. With several endpoints, each message gets a TagKey
// field naming its endpoint.
type Poller struct {
	Client    *http.Client
	Endpoints []Endpoint
	Interval  time.Duration
	Log       *slog.Logger
}

// Run polls until ctx is cancelled. Failed polls (game closed, between
// matches) emit nothing; the source state is logged only when it changes.
func (p *Poller) Run(ctx context.Context, emit func([]byte)) error {
	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()

	up, first := false, true
	for {
		ok, err := p.poll(ctx, emit)
		switch {
		case ctx.Err() != nil:
			return nil
		case !ok:
			if up || first {
				p.Log.Info("source unavailable, waiting", "err", err)
			}
			up = false
		case !up:
			p.Log.Info("source connected")
			up = true
		}
		first = false

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// poll fetches every endpoint once and emits each response that arrives.
// It reports whether any did, and the first error.
func (p *Poller) poll(ctx context.Context, emit func([]byte)) (bool, error) {
	var ok bool
	var firstErr error
	for _, ep := range p.Endpoints {
		body, err := p.fetch(ctx, ep.URL)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(p.Endpoints) > 1 {
			body = tag(body, ep.Name)
		}
		emit(body)
		ok = true
	}
	return ok, firstErr
}

// tag adds TagKey: name as the first field of a JSON object, leaving every
// other byte as the game sent it. Anything that isn't a JSON object is
// returned unchanged.
func tag(body []byte, name string) []byte {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return body
	}
	value, _ := json.Marshal(name)
	rest := bytes.TrimLeft(trimmed[1:], " \t\r\n")
	out := append([]byte(`{"`+TagKey+`":`), value...)
	if rest[0] != '}' {
		out = append(out, ',')
	}
	return append(out, rest...)
}

func (p *Poller) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxPollBody))
}

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: requestTimeout}
}

// newRiotClient returns a client for League's live client data API, which
// serves a self-signed Riot certificate. Verification is skipped, but the
// client refuses to dial anything other than lolHost, so the exception
// cannot leak to any other address.
func newRiotClient() *http.Client {
	dialer := &net.Dialer{Timeout: requestTimeout}
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr != lolHost {
					return nil, fmt.Errorf("riot client may only dial %s, not %s", lolHost, addr)
				}
				return dialer.DialContext(ctx, network, addr)
			},
			Proxy: nil,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
