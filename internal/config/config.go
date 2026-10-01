// Package config holds GameState's user-facing settings. Defaults are
// chosen so that an untouched install works: only the game needs picking.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/fourcourtjester/single-studio-gamestate/internal/adapter"
)

// Default ports. Provisional until the handoff's port question is settled;
// the relay port must match the Single Studio plugin's default.
const (
	DefaultPort     = 47600 // relay WebSocket the browser connects to
	DefaultGSIPort  = 47601 // CS2 / Dota 2 GSI receiver
	DefaultApexPort = 7777  // Apex LiveAPI server (the port LiveAPI docs use)
)

// MinInterval caps polling at 20 Hz.
const MinInterval = 50 * time.Millisecond

// Config is GameState's configuration, loadable from JSON.
type Config struct {
	Game           string   `json:"game"`
	Bind           string   `json:"bind"`
	Port           int      `json:"port"`
	Interval       Duration `json:"interval"`
	GSIPort        int      `json:"gsiPort"`
	ApexPort       int      `json:"apexPort"`
	SC2URL         string   `json:"sc2Url"`
	AllowedOrigins []string `json:"allowedOrigins"`
}

// Default returns the default configuration. Everything binds to
// 127.0.0.1 so GameState is never exposed on the network.
func Default() Config {
	return Config{
		Bind:           "127.0.0.1",
		Port:           DefaultPort,
		Interval:       Duration(time.Second),
		GSIPort:        DefaultGSIPort,
		ApexPort:       DefaultApexPort,
		SC2URL:         "http://127.0.0.1:6119",
		AllowedOrigins: []string{"*"},
	}
}

// Load reads a JSON config file over the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Validate reports the first problem with the configuration.
func (c Config) Validate() error {
	if _, ok := adapter.Lookup(c.Game); c.Game != "" && !ok {
		return fmt.Errorf("unknown game %q", c.Game)
	}
	if net.ParseIP(c.Bind) == nil {
		return fmt.Errorf("bind %q is not an IP address", c.Bind)
	}
	ports := map[string]int{"port": c.Port, "gsiPort": c.GSIPort, "apexPort": c.ApexPort}
	for name, p := range ports {
		if p < 1 || p > 65535 {
			return fmt.Errorf("%s %d out of range", name, p)
		}
	}
	if c.Port == c.GSIPort || c.Port == c.ApexPort {
		return fmt.Errorf("relay port %d collides with an adapter port", c.Port)
	}
	if time.Duration(c.Interval) < MinInterval {
		return fmt.Errorf("interval %s is below the %s minimum", time.Duration(c.Interval), MinInterval)
	}
	return nil
}

// Loopback reports whether GameState only listens on this machine.
func (c Config) Loopback() bool {
	ip := net.ParseIP(c.Bind)
	return ip != nil && ip.IsLoopback()
}

// Duration is a time.Duration that reads and writes JSON as "1s", "250ms".
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("interval must be a string like \"1s\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// State is what GameState remembers between runs.
type State struct {
	Game  string `json:"game"`
	Theme string `json:"theme,omitempty"` // "light" or "dark" (the default)
}

// StatePath returns where State is kept in the user's config directory.
func StatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Single Studio - GameState", "state.json"), nil
}

// LoadState reads remembered state. A missing or unreadable file is an
// empty state: it only saves the user re-picking their game.
func LoadState(path string) State {
	var s State
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &s)
	}
	return s
}

// SaveState writes remembered state.
func SaveState(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// UpdateState changes remembered state in place, keeping fields fn leaves alone.
func UpdateState(path string, fn func(*State)) error {
	s := LoadState(path)
	fn(&s)
	return SaveState(path, s)
}
