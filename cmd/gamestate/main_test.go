package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

func TestFlagsOverrideConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(path, []byte(`{"game":"lol","port":40000}`), 0o644)

	cfg, _, err := parseConfig([]string{"-config", path, "-port", "40001"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Game != "lol" || cfg.Port != 40001 {
		t.Fatalf("got game=%s port=%d", cfg.Game, cfg.Port)
	}
}

func TestGSIConfigCommand(t *testing.T) {
	var out strings.Builder
	err := run(context.Background(), []string{"gsi-config", "-game", "cs2", "-gsi-port", "5000"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "http://127.0.0.1:5000/") || !strings.Contains(out.String(), "game/csgo/cfg") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func TestServeStopsOnCancel(t *testing.T) {
	args := []string{"-no-window", "-game", "cs2", "-port", freePort(t), "-gsi-port", freePort(t)}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error)
	go func() { errc <- run(ctx, args, nil) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop")
	}
}
