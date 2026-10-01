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
	"github.com/fourcourtjester/single-studio-gamestate/internal/gsi"
	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
)

const usage = `Single Studio Companion

Usage:
  companion [flags]             run the relay for one game
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
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	if printGSI {
		return printGSIConfig(cfg, stdout)
	}
	return serve(ctx, cfg)
}

// parseConfig builds the config from defaults, an optional -config file,
// then flags. Flags always win over the file.
func parseConfig(args []string) (config.Config, error) {
	cfg := config.Default()
	path, err := parseFlags(&cfg, args)
	if err != nil {
		return cfg, err
	}
	if path != "" {
		if cfg, err = config.Load(path); err != nil {
			return cfg, err
		}
		if _, err := parseFlags(&cfg, args); err != nil {
			return cfg, err
		}
	}
	return cfg, cfg.Validate()
}

func parseFlags(cfg *config.Config, args []string) (string, error) {
	fs := flag.NewFlagSet("companion", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	path := fs.String("config", "", "path to a JSON config file")
	fs.StringVar(&cfg.Game, "game", cfg.Game, "game namespace to relay")
	fs.StringVar(&cfg.Bind, "bind", cfg.Bind, "address every listener binds to")
	fs.IntVar(&cfg.Port, "port", cfg.Port, "relay WebSocket port Single Studio connects to")
	fs.DurationVar((*time.Duration)(&cfg.Interval), "interval", time.Duration(cfg.Interval), "poll interval (sc2, lol)")
	fs.IntVar(&cfg.GSIPort, "gsi-port", cfg.GSIPort, "GSI receiver port (cs2, dota2)")
	fs.StringVar(&cfg.GSIToken, "gsi-token", cfg.GSIToken, "GSI auth token to require (cs2, dota2)")
	fs.IntVar(&cfg.ApexPort, "apex-port", cfg.ApexPort, "LiveAPI WebSocket server port (apex)")
	fs.StringVar(&cfg.SC2URL, "sc2-url", cfg.SC2URL, "StarCraft II client API base URL (sc2)")
	err := fs.Parse(args)
	return *path, err
}

func printGSIConfig(cfg config.Config, w io.Writer) error {
	uri := "http://" + net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.GSIPort)) + "/"
	out, err := gsi.Config(cfg.Game, uri, cfg.GSIToken)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "// Save as <%s install>/%s/%s\n", cfg.Game, gsi.InstallDir[cfg.Game], gsi.FileName)
	_, err = io.WriteString(w, out)
	return err
}

func serve(ctx context.Context, cfg config.Config) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if !cfg.Loopback() {
		log.Warn("binding beyond loopback: the companion is reachable from the network", "bind", cfg.Bind)
	}

	a, err := adapter.New(cfg.Game, adapter.Options{
		Bind:     cfg.Bind,
		Interval: time.Duration(cfg.Interval),
		GSIPort:  cfg.GSIPort,
		GSIToken: cfg.GSIToken,
		ApexPort: cfg.ApexPort,
		SC2URL:   cfg.SC2URL,
		Log:      log,
	})
	if err != nil {
		return err
	}

	hub := relay.NewHub(cfg.AllowedOrigins, log)
	mux := http.NewServeMux()
	mux.Handle("GET /ws", hub)
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Game string `json:"game"`
			relay.Stats
		}{cfg.Game, hub.Stats()})
	})
	// The config UI will be served from "/"; until then, point at the socket.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Single Studio Companion relaying %q on ws://%s/ws\n", cfg.Game, r.Host)
	})

	addr := net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Info("relay listening", "ws", "ws://"+ln.Addr().String()+"/ws", "game", cfg.Game)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)
	go func() {
		errc <- a.Run(ctx, func(data []byte) { hub.Publish(cfg.Game, data) })
	}()
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	cancel()
	shutdownCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	srv.Shutdown(shutdownCtx)
	log.Info("stopped")
	return err
}
