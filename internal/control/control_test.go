package control

import (
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fourcourtjester/single-studio-gamestate/internal/adapter"
	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
)

func setup(t *testing.T, opts adapter.Options) (*Controller, *ErrorLog) {
	t.Helper()
	errs := &ErrorLog{}
	log := slog.New(errs.Handler(slog.NewTextHandler(io.Discard, nil)))
	opts.Bind = "127.0.0.1"
	if opts.Interval == 0 {
		opts.Interval = time.Hour
	}
	c := New(opts, relay.NewHub(nil, log), log)
	t.Cleanup(c.Stop)
	return c, errs
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestStartNeedsAGame(t *testing.T) {
	c, errs := setup(t, adapter.Options{})
	if err := c.Start(); err == nil {
		t.Fatal("expected an error with no game selected")
	}
	if got := errs.Entries(); len(got) != 1 || !strings.Contains(got[0].Message, "pick a game") {
		t.Fatalf("error pane: %+v", got)
	}
}

func TestStartStopAndSwitch(t *testing.T) {
	c, _ := setup(t, adapter.Options{})
	c.SetPort(adapter.CS2, freePort(t))
	c.SetPort(adapter.SC2, 1) // nothing answers there; SC2 just waits
	var remembered string
	c.OnSelect = func(g string) { remembered = g }

	if err := c.Select(adapter.CS2); err != nil {
		t.Fatal(err)
	}
	if c.State().Running {
		t.Fatal("selecting a game must not turn GameState on")
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if s := c.State(); !s.Running || s.Game != adapter.CS2 {
		t.Fatalf("after start: %+v", s)
	}

	// Switching while on restarts with the new game.
	if err := c.Select(adapter.SC2); err != nil {
		t.Fatal(err)
	}
	if s := c.State(); !s.Running || s.Game != adapter.SC2 || remembered != adapter.SC2 {
		t.Fatalf("after switch: %+v, remembered %q", s, remembered)
	}

	c.Stop()
	if c.State().Running {
		t.Fatal("still running after stop")
	}
}

func TestStopReleasesPorts(t *testing.T) {
	c, _ := setup(t, adapter.Options{})
	c.SetPort(adapter.CS2, freePort(t))
	c.Select(adapter.CS2)
	for range 3 {
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		c.Stop()
	}
	c.Start()
	time.Sleep(50 * time.Millisecond)
	if !c.State().Running {
		t.Fatal("restart failed: port not released")
	}
}

func TestAdapterFailureTurnsOffAndReports(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	c, errs := setup(t, adapter.Options{})
	c.SetPort(adapter.Dota2, busy.Addr().(*net.TCPAddr).Port)
	c.Select(adapter.Dota2)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !c.State().Running })

	got := errs.Entries()
	if len(got) != 1 || !strings.HasPrefix(got[0].Message, "Dota 2 stopped: port ") {
		t.Fatalf("error pane: %+v", got)
	}
}

func TestUnavailableGameReports(t *testing.T) {
	c, errs := setup(t, adapter.Options{})
	c.Select(adapter.War3)
	if err := c.Start(); err == nil {
		t.Fatal("expected war3 to fail")
	}
	if got := errs.Entries(); len(got) != 1 || !strings.Contains(got[0].Message, "Warcraft III") {
		t.Fatalf("error pane: %+v", got)
	}
}

func TestErrorLogFoldsRepeats(t *testing.T) {
	var l ErrorLog
	now := time.Now()
	l.Add(now, "a")
	l.Add(now, "b")
	l.Add(now, "b")
	got := l.Entries()
	if len(got) != 2 || got[0].Message != "b" || got[0].Count != 2 || got[1].Message != "a" {
		t.Fatalf("got %+v", got)
	}
	for i := range maxEntries + 10 {
		l.Add(now, string(rune('A'+i)))
	}
	if n := len(l.Entries()); n != maxEntries {
		t.Fatalf("kept %d entries, want %d", n, maxEntries)
	}
	l.Clear()
	if n := len(l.Entries()); n != 0 {
		t.Fatalf("kept %d entries after clear", n)
	}
}

func TestCaptureHandlerFormats(t *testing.T) {
	var l ErrorLog
	log := slog.New(l.Handler(slog.NewTextHandler(io.Discard, nil))).With("game", "cs2")
	log.Info("not captured")
	log.Warn("websocket accept failed", "err", io.EOF, "origin", "https://x")
	got := l.Entries()
	if len(got) != 1 || got[0].Message != "websocket accept failed: EOF (origin=https://x)" {
		t.Fatalf("got %+v", got)
	}
}

func TestSetPortRestartsRunningGame(t *testing.T) {
	c, _ := setup(t, adapter.Options{})
	first, second := freePort(t), freePort(t)
	if err := c.SetPort(adapter.CS2, first); err != nil {
		t.Fatal(err)
	}
	c.Select(adapter.CS2)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	waitListening(t, first)

	if err := c.SetPort(adapter.CS2, second); err != nil {
		t.Fatal(err)
	}
	waitListening(t, second)
	if c.Port(adapter.CS2) != second || !c.State().Running {
		t.Fatalf("port=%d running=%v", c.Port(adapter.CS2), c.State().Running)
	}
	// Another game's port doesn't touch the running one.
	if err := c.SetPort(adapter.Apex, 9999); err != nil || c.Port(adapter.Apex) != 9999 {
		t.Fatalf("apex: %v %d", err, c.Port(adapter.Apex))
	}
}

func TestSetPortRules(t *testing.T) {
	c, _ := setup(t, adapter.Options{})
	if err := c.SetPort(adapter.LoL, 3000); err == nil {
		t.Error("League's port is fixed; expected an error")
	}
	if err := c.SetPort(adapter.RL, 0); err == nil {
		t.Error("expected an out-of-range error")
	}
	c.SetPort(adapter.RL, 50000)
	c.SetPort(adapter.RL, 49124) // back to the default clears the override
	if _, ok := c.Ports()[adapter.RL]; ok || c.Port(adapter.RL) != 49124 {
		t.Fatalf("overrides %v", c.Ports())
	}
}

func waitListening(t *testing.T, port int) {
	t.Helper()
	waitFor(t, func() bool {
		conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			conn.Close()
		}
		return err == nil
	})
}
