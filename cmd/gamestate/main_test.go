package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
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

func TestGamePortFlag(t *testing.T) {
	cfg, _, err := parseConfig([]string{"-game", "rl", "-game-port", "50124"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GamePort("rl") != 50124 || cfg.GamePort("apex") != 7777 {
		t.Fatalf("got rl=%d apex=%d", cfg.GamePort("rl"), cfg.GamePort("apex"))
	}
	if _, _, err := parseConfig([]string{"-game-port", "50124"}); err == nil {
		t.Error("-game-port without -game should fail")
	}
	if _, _, err := parseConfig([]string{"-game", "lol", "-game-port", "3000"}); err == nil {
		t.Error("League's port is fixed; -game-port should fail")
	}
}

func TestOverlaysConnectAtBareAddress(t *testing.T) {
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error)
	go func() { errc <- run(ctx, []string{"-no-window", "-port", port}) }()
	defer func() { cancel(); <-errc }()

	for _, path := range []string{"", "/"} {
		url := "ws://127.0.0.1:" + port + path
		var conn *websocket.Conn
		var err error
		for range 50 { // the server may still be starting
			dctx, dcancel := context.WithTimeout(context.Background(), time.Second)
			conn, _, err = websocket.Dial(dctx, url, nil)
			dcancel()
			if err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		conn.CloseNow()
	}
}

func TestServeStopsOnCancel(t *testing.T) {
	args := []string{"-no-window", "-game", "cs2", "-port", freePort(t), "-game-port", freePort(t)}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error)
	go func() { errc <- run(ctx, args) }()
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
