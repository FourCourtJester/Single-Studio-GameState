// Command gamestate is Single Studio - GameState: a local relay that reads
// game state feeds a browser cannot reach and pushes the raw payloads to
// Single Studio over one WebSocket.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fourcourtjester/single-studio-gamestate/internal/adapter"
	"github.com/fourcourtjester/single-studio-gamestate/internal/config"
	"github.com/fourcourtjester/single-studio-gamestate/internal/control"
	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
)

const usage = `Single Studio - GameState

Usage:
  gamestate [flags]  run GameState and open its window

Games: apex, cs2, dota2, lol, rl, sc2, war3

Flags:
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "gamestate:", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	cfg, noWindow, err := parseConfig(args)
	if err != nil {
		return err
	}
	return serve(ctx, cfg, !noWindow)
}

// parseConfig builds the config from defaults, an optional -config file,
// then flags. Flags always win over the file.
func parseConfig(args []string) (cfg config.Config, noWindow bool, err error) {
	cfg = config.Default()
	f, err := parseFlags(&cfg, args)
	if err != nil {
		return cfg, f.noWindow, err
	}
	if f.path != "" {
		if cfg, err = config.Load(f.path); err != nil {
			return cfg, f.noWindow, err
		}
		if f, err = parseFlags(&cfg, args); err != nil {
			return cfg, f.noWindow, err
		}
	}
	if f.gamePort != 0 {
		if cfg.Game == "" {
			return cfg, f.noWindow, errors.New("-game-port needs -game")
		}
		cfg.GamePorts = maps.Clone(cfg.GamePorts)
		if cfg.GamePorts == nil {
			cfg.GamePorts = map[string]int{}
		}
		cfg.GamePorts[cfg.Game] = f.gamePort
	}
	return cfg, f.noWindow, cfg.Validate()
}

// extraFlags are the flags that aren't settings.
type extraFlags struct {
	path     string
	noWindow bool
	gamePort int
}

func parseFlags(cfg *config.Config, args []string) (extraFlags, error) {
	fs := flag.NewFlagSet("gamestate", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	path := fs.String("config", "", "path to a JSON config file")
	noWindow := fs.Bool("no-window", false, "run without a window (headless)")
	fs.StringVar(&cfg.Game, "game", cfg.Game, "game to start relaying immediately")
	fs.StringVar(&cfg.Bind, "bind", cfg.Bind, "address every listener binds to")
	fs.IntVar(&cfg.Port, "port", cfg.Port, "relay WebSocket port Single Studio connects to")
	fs.DurationVar((*time.Duration)(&cfg.Interval), "interval", time.Duration(cfg.Interval), "poll interval (sc2, lol)")
	gamePort := fs.Int("game-port", 0, "port for the -game game's feed, if not its default")
	err := fs.Parse(args)
	return extraFlags{*path, *noWindow, *gamePort}, err
}

// serve runs the relay, with the window unless gui is false.
func serve(ctx context.Context, cfg config.Config, gui bool) error {
	errs := &control.ErrorLog{}
	log := slog.New(errs.Handler(slog.NewTextHandler(os.Stderr, nil)))
	if !cfg.Loopback() {
		log.Warn("binding beyond loopback: GameState is reachable from the network", "bind", cfg.Bind)
	}

	// Ports picked in the window are remembered, and used unless a flag or
	// config file chose that port.
	statePath, _ := config.StatePath()
	state := config.State{}
	if statePath != "" {
		state = config.LoadState(statePath)
	}
	for game, port := range state.GamePorts {
		if _, set := cfg.GamePorts[game]; set {
			continue
		}
		withSaved := cfg
		withSaved.GamePorts = maps.Clone(cfg.GamePorts)
		if withSaved.GamePorts == nil {
			withSaved.GamePorts = map[string]int{}
		}
		withSaved.GamePorts[game] = port
		if withSaved.Validate() == nil {
			cfg = withSaved
		}
	}
	if cfg.Port == config.DefaultPort && state.Port != 0 {
		withSaved := cfg
		withSaved.Port = state.Port
		if withSaved.Validate() == nil {
			cfg = withSaved
		}
	}
	remember := func(fn func(*config.State)) {
		if statePath == "" {
			return
		}
		if err := config.UpdateState(statePath, fn); err != nil {
			log.Warn("couldn't save settings", "err", err)
		}
	}

	hub := relay.NewHub(cfg.AllowedOrigins, log)
	ctrl := control.New(adapter.Options{
		Bind:     cfg.Bind,
		Interval: time.Duration(cfg.Interval),
	}, hub, log)
	for game, port := range cfg.GamePorts {
		ctrl.SetPort(game, port)
	}

	var show atomic.Pointer[func()]
	mux := http.NewServeMux()
	mux.Handle("GET /ws", hub)
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			control.State
			relay.Stats
		}{ctrl.State(), hub.Stats()})
	})
	// A second launch asks this one to bring its window forward. Browsers
	// always send Origin on cross-site POSTs, so web pages can't do this.
	mux.HandleFunc("POST /show", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if f := show.Load(); f != nil {
			(*f)()
		}
		w.WriteHeader(http.StatusNoContent)
	})

	rs := newRelayServer(cfg.Bind, mux, hub, log)
	defer rs.Close()
	if err := rs.Listen(cfg.Port); err != nil {
		// Most likely GameState is already running: bring its window
		// forward instead of opening a second one.
		if gui && showRunning(net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port))) {
			return nil
		}
		if !gui {
			return err
		}
		// Without a console the window is the only place to say what's
		// wrong; the user can pick another port there.
		log.Error("can't open the broadcast port", "err", err)
	}

	// -game starts relaying straight away; otherwise preselect the game the
	// user picked last time and wait for them to switch it on.
	if cfg.Game != "" {
		ctrl.Select(cfg.Game)
		if rs.Port() != 0 {
			ctrl.Start()
		}
	} else {
		ctrl.Select(state.Game)
	}
	ctrl.OnSelect = func(game string) { remember(func(s *config.State) { s.Game = game }) }

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var err error
	if gui {
		runWindow(ctx, window{
			ctrl:  ctrl,
			errs:  errs,
			hub:   hub,
			log:   log,
			relay: rs,
			bind:  cfg.Bind,
			dark:  state.Theme != "light",
			onTheme: func(dark bool) {
				remember(func(s *config.State) { s.Theme = map[bool]string{true: "dark", false: "light"}[dark] })
			},
			onPort: func(port int) error {
				current := cfg
				current.GamePorts = ctrl.Ports()
				if game := current.PortUser(port); game != "" {
					err := fmt.Errorf("port %d is %s's port", port, game)
					log.Error("can't move the broadcast port", "err", err)
					return err
				}
				if err := rs.Listen(port); err != nil {
					log.Error("can't move the broadcast port", "err", err)
					return err
				}
				remember(func(s *config.State) { s.Port = port })
				return nil
			},
			onGamePort: func(game string, port int) error {
				if port == rs.Port() {
					err := fmt.Errorf("port %d is the broadcast port", port)
					log.Error("can't change the game port", "err", err)
					return err
				}
				if err := ctrl.SetPort(game, port); err != nil {
					log.Error("can't change the game port", "err", err)
					return err
				}
				remember(func(s *config.State) { s.GamePorts = ctrl.Ports() })
				return nil
			},
			show: &show,
		})
	} else {
		select {
		case <-ctx.Done():
		case err = <-rs.errc:
		}
	}

	ctrl.Stop()
	log.Info("stopped")
	return err
}

// showRunning asks a GameState instance already listening on addr to bring its
// window forward, and reports whether one answered.
func showRunning(addr string) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Post("http://"+addr+"/show", "text/plain", nil)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
}
