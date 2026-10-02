// Package control runs the chosen game's adapter and lets the UI turn it on
// and off or switch games while the relay keeps serving.
package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/fourcourtjester/single-studio-gamestate/internal/adapter"
	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
)

// State is what the UI shows.
type State struct {
	Game     string     `json:"game"`
	Running  bool       `json:"running"`
	LastData *time.Time `json:"lastData,omitempty"`
}

// Controller owns the one running adapter.
type Controller struct {
	opts adapter.Options
	hub  *relay.Hub
	log  *slog.Logger

	// OnSelect is called when the user picks a game, so it can be remembered.
	OnSelect func(game string)

	op sync.Mutex // serialises Select, Start and Stop

	mu       sync.Mutex
	ports    map[string]int // per-game overrides of the title's default port
	game     string
	running  bool
	cancel   context.CancelFunc
	done     chan struct{}
	lastData time.Time
}

// New returns a stopped Controller that publishes to hub.
func New(opts adapter.Options, hub *relay.Hub, log *slog.Logger) *Controller {
	opts.Log = log
	return &Controller{opts: opts, hub: hub, log: log, ports: map[string]int{}}
}

// Port returns the port used for game: the user's override, else the
// title's default. 0 means the game's port is fixed.
func (c *Controller) Port(game string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.port(game)
}

func (c *Controller) port(game string) int {
	if p, ok := c.ports[game]; ok {
		return p
	}
	t, _ := adapter.Lookup(game)
	return t.DefaultPort
}

// Ports returns the per-game overrides, for saving.
func (c *Controller) Ports() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.ports)
}

// SetPort changes game's port. If that game is running it restarts on the
// new port; a port that can't be used is reported like any start failure.
func (c *Controller) SetPort(game string, port int) error {
	t, ok := adapter.Lookup(game)
	switch {
	case !ok:
		return fmt.Errorf("unknown game %q", game)
	case t.DefaultPort == 0:
		return fmt.Errorf("%s's port can't be changed", t.Name)
	case port < 1 || port > 65535:
		return fmt.Errorf("port %d out of range", port)
	}
	c.op.Lock()
	defer c.op.Unlock()

	c.mu.Lock()
	if port == t.DefaultPort {
		delete(c.ports, game)
	} else {
		c.ports[game] = port
	}
	restart := c.running && c.game == game
	c.mu.Unlock()

	if restart {
		return c.start()
	}
	return nil
}

// State returns a snapshot for the UI.
func (c *Controller) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := State{Game: c.game, Running: c.running}
	if c.running && !c.lastData.IsZero() {
		t := c.lastData
		s.LastData = &t
	}
	return s
}

// Select picks the game. If GameState is on, it switches over at once.
func (c *Controller) Select(game string) error {
	if _, ok := adapter.Lookup(game); !ok {
		return fmt.Errorf("unknown game %q", game)
	}
	c.op.Lock()
	defer c.op.Unlock()

	c.mu.Lock()
	changed, running := c.game != game, c.running
	c.game = game
	c.mu.Unlock()

	if changed {
		// The hub's latest message belongs to the old game; don't send it
		// to clients that connect from now on.
		c.hub.Forget()
		if c.OnSelect != nil {
			c.OnSelect(game)
		}
	}
	if changed && running {
		return c.start()
	}
	return nil
}

// Start turns GameState on for the selected game. Failures are logged,
// which puts them in the error pane, as well as returned.
func (c *Controller) Start() error {
	c.op.Lock()
	defer c.op.Unlock()
	return c.start()
}

// Stop turns GameState off and waits for the adapter to let go of its
// ports.
func (c *Controller) Stop() {
	c.op.Lock()
	defer c.op.Unlock()
	c.stop()
}

func (c *Controller) start() error {
	c.stop()

	c.mu.Lock()
	game := c.game
	c.mu.Unlock()

	title, ok := adapter.Lookup(game)
	if !ok {
		err := errors.New("pick a game first")
		c.log.Warn("can't turn on", "err", err)
		return err
	}
	opts := c.opts
	c.mu.Lock()
	opts.Port = c.port(game)
	c.mu.Unlock()
	a, err := adapter.New(game, opts)
	if err != nil {
		c.log.Error(fmt.Sprintf("can't relay %s", title.Name), "err", err)
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.mu.Lock()
	c.running, c.cancel, c.done, c.lastData = true, cancel, done, time.Time{}
	c.mu.Unlock()

	emit := func(data []byte) {
		c.mu.Lock()
		c.lastData = time.Now()
		c.mu.Unlock()
		c.hub.Publish(data)
	}
	go func() {
		defer close(done)
		err := a.Run(ctx, emit)
		if ctx.Err() != nil {
			return // stopped on purpose
		}
		c.mu.Lock()
		if c.done == done {
			c.running, c.cancel, c.done = false, nil, nil
		}
		c.mu.Unlock()
		cancel()
		if err == nil {
			err = errors.New("adapter exited")
		}
		c.log.Error(fmt.Sprintf("%s stopped", title.Name), "err", err)
	}()
	c.log.Info("relaying", "game", game)
	return nil
}

func (c *Controller) stop() {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.running, c.cancel, c.done = false, nil, nil
	c.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}
