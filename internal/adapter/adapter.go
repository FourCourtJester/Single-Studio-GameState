// Package adapter holds the only game-specific code in GameState:
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
	RL    = "rl"
	War3  = "war3"
)

// Title is a game GameState knows about, as shown in the game picker.
type Title struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

// Titles lists every game, in picker order.
var Titles = []Title{
	{Apex, "Apex Legends", true},
	{CS2, "Counter-Strike 2", true},
	{Dota2, "Dota 2", true},
	{LoL, "League of Legends", true},
	{RL, "Rocket League", true},
	{SC2, "StarCraft II", true},
	{War3, "Warcraft III", false},
}

// Lookup returns the title for a namespace.
func Lookup(id string) (Title, bool) {
	for _, t := range Titles {
		if t.ID == id {
			return t, true
		}
	}
	return Title{}, false
}

// ErrNotImplemented is returned for titles whose transport is not yet known.
var ErrNotImplemented = errors.New("adapter not implemented")

// Options carries the settings adapters need. Only the fields relevant to
// the chosen game are read.
type Options struct {
	Bind     string        // interface receive adapters listen on
	Interval time.Duration // poll interval for poll adapters
	GSIPort  int           // CS2 / Dota 2 Game State Integration receiver
	ApexPort int           // Apex LiveAPI WebSocket server
	RLPort   int           // Rocket League Stats API socket the game serves
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
			Addr: net.JoinHostPort(o.Bind, strconv.Itoa(o.GSIPort)),
			Log:  log,
		}, nil
	case Apex:
		return &WSServer{
			Addr: net.JoinHostPort(o.Bind, strconv.Itoa(o.ApexPort)),
			Log:  log,
		}, nil
	case RL:
		// The game serves the socket on this machine; bind doesn't apply.
		return &TCPStream{
			Addr:  net.JoinHostPort("127.0.0.1", strconv.Itoa(o.RLPort)),
			Log:   log,
			Retry: 2 * time.Second,
		}, nil
	case War3:
		// Observer data is known to exist, but its transport is unverified.
		return nil, fmt.Errorf("%s: %w (transport still to be confirmed)", game, ErrNotImplemented)
	default:
		return nil, fmt.Errorf("unknown game %q", game)
	}
}
