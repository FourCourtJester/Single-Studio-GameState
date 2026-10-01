package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Game = "sc2"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.Loopback() {
		t.Fatal("default bind must be loopback")
	}
}

func TestValidate(t *testing.T) {
	bad := map[string]func(*Config){
		"unknown game":   func(c *Config) { c.Game = "valorant" },
		"bad bind":       func(c *Config) { c.Bind = "localhost" },
		"port range":     func(c *Config) { c.Port = 70000 },
		"port collision": func(c *Config) { c.GSIPort = c.Port },
		"rl collision":   func(c *Config) { c.RLPort = c.Port },
		"fast interval":  func(c *Config) { c.Interval = Duration(10 * time.Millisecond) },
	}
	for name, mutate := range bad {
		cfg := Default()
		cfg.Game = "cs2"
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLoadOverDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gamestate.json")
	os.WriteFile(path, []byte(`{"game":"lol","interval":"250ms"}`), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Game != "lol" || time.Duration(cfg.Interval) != 250*time.Millisecond {
		t.Fatalf("file values not applied: %+v", cfg)
	}
	if cfg.Port != DefaultPort || cfg.Bind != "127.0.0.1" {
		t.Fatalf("defaults lost: %+v", cfg)
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	if got := LoadState(path); got.Game != "" {
		t.Fatalf("missing file: got %+v", got)
	}
	if err := SaveState(path, State{Game: "apex"}); err != nil {
		t.Fatal(err)
	}
	if got := LoadState(path); got.Game != "apex" {
		t.Fatalf("got %+v", got)
	}
}

func TestUpdateStateKeepsOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	SaveState(path, State{Game: "sc2", Theme: "light"})
	UpdateState(path, func(s *State) { s.Game = "lol" })
	if got := LoadState(path); got.Game != "lol" || got.Theme != "light" {
		t.Fatalf("got %+v", got)
	}
}
