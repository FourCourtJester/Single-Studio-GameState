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
	path, noWindow, err := parseFlags(&cfg, args)
	if err != nil {
		return cfg, noWindow, err
	}
	if path != "" {
		if cfg, err = config.Load(path); err != nil {
			return cfg, noWindow, err
		}
		if _, _, err := parseFlags(&cfg, args); err != nil {
			return cfg, noWindow, err
		}
	}
	return cfg, noWindow, cfg.Validate()
}

func parseFlags(cfg *config.Config, args []string) (string, bool, error) {
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
	fs.IntVar(&cfg.GSIPort, "gsi-port", cfg.GSIPort, "GSI receiver port (cs2, dota2)")
	fs.IntVar(&cfg.ApexPort, "apex-port", cfg.ApexPort, "LiveAPI WebSocket server port (apex)")
	fs.IntVar(&cfg.RLPort, "rl-port", cfg.RLPort, "Stats API port the game serves (rl)")
	fs.StringVar(&cfg.SC2URL, "sc2-url", cfg.SC2URL, "StarCraft II client API base URL (sc2)")
	err := fs.Parse(args)
	return *path, *noWindow, err
}

// serve runs the relay, with the window unless gui is false.
func serve(ctx context.Context, cfg config.Config, gui bool) error {
	errs := &control.ErrorLog{}
	log := slog.New(errs.Handler(slog.NewTextHandler(os.Stderr, nil)))
	if !cfg.Loopback() {
		log.Warn("binding beyond loopback: GameState is reachable from the network", "bind", cfg.Bind)
	}

	// A broadcast port picked in the window is remembered, and used unless
	// -port or a config file chose one.
	statePath, _ := config.StatePath()
	state := config.State{}
	if statePath != "" {
		state = config.LoadState(statePath)
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
		GSIPort:  cfg.GSIPort,
		ApexPort: cfg.ApexPort,
		RLPort:   cfg.RLPort,
		SC2URL:   cfg.SC2URL,
	}, hub, log)

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
			ctrl:   ctrl,
			errs:   errs,
			hub:    hub,
			log:    log,
			relay:  rs,
			bind:   cfg.Bind,
			gsiURL: "http://" + net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.GSIPort)) + "/",
			rlPort: cfg.RLPort,
			dark:   state.Theme != "light",
			onTheme: func(dark bool) {
				remember(func(s *config.State) { s.Theme = map[bool]string{true: "dark", false: "light"}[dark] })
			},
			onPort: func(port int) error {
				if port == cfg.GSIPort || port == cfg.ApexPort || port == cfg.RLPort {
					err := fmt.Errorf("port %d is used for receiving game state", port)
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
