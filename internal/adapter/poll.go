package adapter

import (
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

// Endpoint is one URL a Poller fetches each tick.
type Endpoint struct {
	Name string
	URL  string
}

// Poller fetches its endpoints on a timer and emits the result.
//
// With one endpoint the response body is emitted verbatim. With several, the
// bodies are combined into one JSON object keyed by endpoint name, so a tick
// is always a single payload.
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
		data, err := p.poll(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			if up || first {
				p.Log.Info("source unavailable, waiting", "err", err)
			}
			up = false
		default:
			if !up {
				p.Log.Info("source connected")
			}
			up = true
			emit(data)
		}
		first = false

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (p *Poller) poll(ctx context.Context) ([]byte, error) {
	if len(p.Endpoints) == 1 {
		return p.fetch(ctx, p.Endpoints[0].URL)
	}
	combined := make(map[string]json.RawMessage, len(p.Endpoints))
	for _, ep := range p.Endpoints {
		body, err := p.fetch(ctx, ep.URL)
		if err != nil {
			return nil, err
		}
		if !json.Valid(body) {
			return nil, fmt.Errorf("%s: response is not JSON", ep.URL)
		}
		combined[ep.Name] = body
	}
	return json.Marshal(combined)
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
