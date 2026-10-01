// Package adapter holds the only game-specific code in the companion:
// acquisition. Each adapter gets data however its title requires (polling,
// receiving HTTP, hosting a WebSocket) and hands raw payloads to emit.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"
)

// Adapter acquires raw payloads for one title.
//
// Run blocks until ctx is cancelled (returning nil) or the adapter cannot
// continue (returning an error). A source that is simply not running yet,
// such as the game being closed, is not an error: the adapter waits for it.
type Adapter interface {
	Run(ctx context.Context, emit func(data []byte)) error
}

// Namespaces, one per supported title. The user's game choice assigns it.
const (
	Apex  = "apex"
	SC2   = "sc2"
	LoL   = "lol"
	CS2   = "cs2"
	Dota2 = "dota2"
	War3  = "war3"
)

// Games lists every namespace the companion knows about.
var Games = []string{Apex, SC2, LoL, CS2, Dota2, War3}

// ErrNotImplemented is returned for titles whose transport is not yet known.
var ErrNotImplemented = errors.New("adapter not implemented")

// Options carries the settings adapters need. Only the fields relevant to
// the chosen game are read.
type Options struct {
	Bind     string        // interface receive adapters listen on
	Interval time.Duration // poll interval for poll adapters
	GSIPort  int           // CS2 / Dota 2 Game State Integration receiver
	GSIToken string        // optional GSI auth token to require
	ApexPort int           // Apex LiveAPI WebSocket server
	SC2URL   string        // StarCraft II client API base URL
	Log      *slog.Logger
}

// New returns the adapter for game.
func New(game string, o Options) (Adapter, error) {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	log := o.Log.With("game", game)

	switch game {
	case SC2:
		return &Poller{
			Client:   newHTTPClient(),
			Interval: o.Interval,
			Endpoints: []Endpoint{
				{Name: "game", URL: o.SC2URL + "/game"},
				{Name: "ui", URL: o.SC2URL + "/ui"},
			},
			Log: log,
		}, nil
	case LoL:
		return &Poller{
			Client:    newRiotClient(),
			Interval:  o.Interval,
			Endpoints: []Endpoint{{URL: lolURL}},
			Log:       log,
		}, nil
	case CS2, Dota2:
		return &Receiver{
			Addr:  net.JoinHostPort(o.Bind, strconv.Itoa(o.GSIPort)),
			Token: o.GSIToken,
			Log:   log,
		}, nil
	case Apex:
		return &WSServer{
			Addr: net.JoinHostPort(o.Bind, strconv.Itoa(o.ApexPort)),
			Log:  log,
		}, nil
	case War3:
		// Observer data is known to exist, but its transport is unverified.
		return nil, fmt.Errorf("%s: %w (transport still to be confirmed)", game, ErrNotImplemented)
	default:
		return nil, fmt.Errorf("unknown game %q", game)
	}
}
