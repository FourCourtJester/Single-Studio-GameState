// Command companion is the Single Studio Companion: a local relay that reads
// game state feeds a browser cannot reach and pushes the raw payloads to
// Single Studio over one WebSocket.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/fourcourtjester/single-studio-gamestate/internal/adapter"
	"github.com/fourcourtjester/single-studio-gamestate/internal/config"
	"github.com/fourcourtjester/single-studio-gamestate/internal/control"
	"github.com/fourcourtjester/single-studio-gamestate/internal/gsi"
	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
	"github.com/fourcourtjester/single-studio-gamestate/internal/ui"
)

const usage = `Single Studio Companion

Usage:
  companion [flags]             run the companion and open its control panel
  companion gsi-config [flags]  print the CS2 / Dota 2 GSI config file

Games: apex, sc2, lol, cs2, dota2, war3

Flags:
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "companion:", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	printGSI := len(args) > 0 && args[0] == "gsi-config"
	if printGSI {
		args = args[1:]
	}
	cfg, noBrowser, err := parseConfig(args)
	if err != nil {
		return err
	}
	if printGSI {
		return printGSIConfig(cfg, stdout)
	}
	return serve(ctx, cfg, !noBrowser)
}

// parseConfig builds the config from defaults, an optional -config file,
// then flags. Flags always win over the file.
func parseConfig(args []string) (cfg config.Config, noBrowser bool, err error) {
	cfg = config.Default()
	path, noBrowser, err := parseFlags(&cfg, args)
	if err != nil {
		return cfg, noBrowser, err
	}
	if path != "" {
		if cfg, err = config.Load(path); err != nil {
			return cfg, noBrowser, err
		}
		if _, _, err := parseFlags(&cfg, args); err != nil {
			return cfg, noBrowser, err
		}
	}
	return cfg, noBrowser, cfg.Validate()
}

func parseFlags(cfg *config.Config, args []string) (string, bool, error) {
	fs := flag.NewFlagSet("companion", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	path := fs.String("config", "", "path to a JSON config file")
	noBrowser := fs.Bool("no-browser", false, "don't open the control panel on start")
	fs.StringVar(&cfg.Game, "game", cfg.Game, "game to start relaying immediately")
	fs.StringVar(&cfg.Bind, "bind", cfg.Bind, "address every listener binds to")
	fs.IntVar(&cfg.Port, "port", cfg.Port, "relay WebSocket port Single Studio connects to")
	fs.DurationVar((*time.Duration)(&cfg.Interval), "interval", time.Duration(cfg.Interval), "poll interval (sc2, lol)")
	fs.IntVar(&cfg.GSIPort, "gsi-port", cfg.GSIPort, "GSI receiver port (cs2, dota2)")
	fs.StringVar(&cfg.GSIToken, "gsi-token", cfg.GSIToken, "GSI auth token to require (cs2, dota2)")
	fs.IntVar(&cfg.ApexPort, "apex-port", cfg.ApexPort, "LiveAPI WebSocket server port (apex)")
	fs.StringVar(&cfg.SC2URL, "sc2-url", cfg.SC2URL, "StarCraft II client API base URL (sc2)")
	err := fs.Parse(args)
	return *path, *noBrowser, err
}

func printGSIConfig(cfg config.Config, w io.Writer) error {
	if cfg.Game == "" {
		return errors.New("gsi-config needs -game cs2 or -game dota2")
	}
	uri := "http://" + net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.GSIPort)) + "/"
	out, err := gsi.Config(cfg.Game, uri, cfg.GSIToken)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "// Save as <%s install>/%s/%s\n", cfg.Game, gsi.InstallDir[cfg.Game], gsi.FileName)
	_, err = io.WriteString(w, out)
	return err
}

func serve(ctx context.Context, cfg config.Config, openBrowser bool) error {
	errs := &control.ErrorLog{}
	log := slog.New(errs.Handler(slog.NewTextHandler(os.Stderr, nil)))
	if !cfg.Loopback() {
		log.Warn("binding beyond loopback: the companion is reachable from the network", "bind", cfg.Bind)
	}

	addr := net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port))
	panel := "http://" + addr + "/"
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Most likely the companion is already running: show its panel.
		if openBrowser && alreadyRunning(panel) {
			fmt.Fprintln(os.Stderr, "companion is already running at", panel)
			return ui.Open(panel)
		}
		return err
	}

	hub := relay.NewHub(cfg.AllowedOrigins, log)
	ctrl := control.New(adapter.Options{
		Bind:     cfg.Bind,
		Interval: time.Duration(cfg.Interval),
		GSIPort:  cfg.GSIPort,
		GSIToken: cfg.GSIToken,
		ApexPort: cfg.ApexPort,
		SC2URL:   cfg.SC2URL,
	}, hub, log)

	// -game starts relaying straight away; otherwise preselect the game the
	// user picked last time and wait for them to switch it on.
	statePath, _ := config.StatePath()
	if cfg.Game != "" {
		ctrl.Select(cfg.Game)
		ctrl.Start()
	} else if statePath != "" {
		ctrl.Select(config.LoadState(statePath).Game)
	}
	if statePath != "" {
		ctrl.OnSelect = func(game string) {
			if err := config.SaveState(statePath, config.State{Game: game}); err != nil {
				log.Warn("couldn't remember the selected game", "err", err)
			}
		}
	}

	mux := http.NewServeMux()
	mux.Handle("GET /ws", hub)
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			control.State
			relay.Stats
		}{ctrl.State(), hub.Stats()})
	})
	(&ui.Server{Ctrl: ctrl, Errors: errs, Hub: hub, Bind: cfg.Bind}).Register(mux)

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Info("companion ready", "panel", panel, "ws", "ws://"+addr+"/ws")
	if openBrowser {
		if err := ui.Open(panel); err != nil {
			log.Info("open the control panel in a browser", "url", panel)
		}
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		err = nil
	case err = <-errc:
	}
	ctrl.Stop()
	shutdownCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	srv.Shutdown(shutdownCtx)
	log.Info("stopped")
	return err
}

// alreadyRunning reports whether a companion answers at panel.
func alreadyRunning(panel string) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(panel + "status")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
