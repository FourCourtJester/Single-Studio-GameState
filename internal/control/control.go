// Package control runs the chosen game's adapter and lets the UI turn it on
// and off or switch games while the relay keeps serving.
package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	game     string
	running  bool
	cancel   context.CancelFunc
	done     chan struct{}
	lastData time.Time
}

// New returns a stopped Controller that publishes to hub.
func New(opts adapter.Options, hub *relay.Hub, log *slog.Logger) *Controller {
	opts.Log = log
	return &Controller{opts: opts, hub: hub, log: log}
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

// Select picks the game. If GameState is on, it switches over at once;
// the old game's namespace is left in place in Single Studio.
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

	if changed && c.OnSelect != nil {
		c.OnSelect(game)
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
	a, err := adapter.New(game, c.opts)
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
		c.hub.Publish(game, data)
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
