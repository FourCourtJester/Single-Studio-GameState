package ui

import (
	"errors"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/fourcourtjester/single-studio-gamestate/internal/adapter"
	"github.com/fourcourtjester/single-studio-gamestate/internal/control"
	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
)

type fixture struct {
	panel *Panel
	ctrl  *control.Controller
	errs  *control.ErrorLog
	win   fyne.Window
	ui    chan func() // UI-thread work queued by the panel's goroutines
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	a := test.NewTempApp(t)
	errs := &control.ErrorLog{}
	log := slog.New(errs.Handler(slog.NewTextHandler(io.Discard, nil)))
	hub := relay.NewHub(nil, log)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	ctrl := control.New(adapter.Options{Bind: "127.0.0.1", Interval: time.Hour}, hub, log)
	t.Cleanup(ctrl.Stop)
	ctrl.SetPort(adapter.CS2, port)
	ctrl.SetPort(adapter.SC2, 1) // nothing answers there; SC2 just waits
	win := a.NewWindow(Title)
	p := NewPanel(a, win, ctrl, errs, hub, Options{
		Bind: "127.0.0.1",
		Port: 47600,
		Dark: true,
	})
	queue := make(chan func(), 100)
	p.do = func(fn func()) { queue <- fn }
	p.Show()
	return fixture{p, ctrl, errs, win, queue}
}

// eventually runs queued UI work and refreshes on the test goroutine,
// which stands in for the UI thread, until cond holds.
func eventually(t *testing.T, f fixture, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for drained := false; !drained; {
			select {
			case fn := <-f.ui:
				fn()
			default:
				drained = true
			}
		}
		f.panel.Refresh()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPowerNeedsAGame(t *testing.T) {
	f := newFixture(t)
	if !f.panel.power.Disabled() {
		t.Fatal("switch should be disabled until a game is picked")
	}
	f.panel.game.SetSelected("Counter-Strike 2")
	eventually(t, f, func() bool { return !f.panel.power.Disabled() })
	if f.ctrl.State().Game != adapter.CS2 {
		t.Fatalf("controller game = %q", f.ctrl.State().Game)
	}
}

func TestSwitchStartsAndStops(t *testing.T) {
	f := newFixture(t)
	f.ctrl.Select(adapter.CS2)
	f.panel.Refresh()

	test.Tap(f.panel.power)
	eventually(t, f, func() bool { return f.panel.powerLabel.Text == "On" })
	if !f.ctrl.State().Running || f.panel.status.Text != "Waiting for Counter-Strike 2…" {
		t.Fatalf("running=%v status=%q", f.ctrl.State().Running, f.panel.status.Text)
	}

	test.Tap(f.panel.power)
	eventually(t, f, func() bool { return f.panel.powerLabel.Text == "Off" })
	if f.ctrl.State().Running {
		t.Fatal("still running")
	}
}

func TestErrorPaneGrowsAndShrinksWindow(t *testing.T) {
	f := newFixture(t)
	if f.panel.errPane.Visible() {
		t.Fatal("error pane should start hidden")
	}
	before := f.win.Canvas().Size().Height

	f.ctrl.Select(adapter.War3) // not implemented: starting it reports an error
	f.ctrl.Start()
	eventually(t, f, func() bool { return f.panel.errPane.Visible() })
	if len(f.panel.errList.Objects) != 1 {
		t.Fatalf("%d error rows", len(f.panel.errList.Objects))
	}
	if grown := f.win.Canvas().Size().Height; grown <= before {
		t.Fatalf("window did not grow: %v -> %v", before, grown)
	}

	f.errs.Clear()
	eventually(t, f, func() bool {
		return !f.panel.errPane.Visible() && abs(f.win.Canvas().Size().Height-before) < 1
	})
}

func TestUserResizedWindowIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	f.errs.Add(time.Now(), "boom")
	eventually(t, f, func() bool { return f.panel.errPane.Visible() })
	time.Sleep(resizeSettle + 50*time.Millisecond)
	f.panel.Refresh() // records the settled height

	big := fyne.NewSize(Width, 700) // the user drags the window taller
	f.win.Resize(big)
	f.errs.Clear()
	for range 3 {
		time.Sleep(resizeSettle)
		f.panel.Refresh()
	}
	if h := f.win.Canvas().Size().Height; h != big.Height {
		t.Fatalf("window resized to %v; the user's size should stick", h)
	}
}

func TestSetupHelpPerGame(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct {
		game string
		want string // "" means no help shown
	}{
		{adapter.Dota2, "http://127.0.0.1:47601/"},
		{adapter.SC2, "port 1"},
		{adapter.RL, "port 49124"},
		{adapter.Apex, "ws://127.0.0.1:7777"},
		{adapter.LoL, ""},
	} {
		f.ctrl.Select(c.game)
		f.panel.Refresh()
		if shown := f.panel.helpBox.Visible(); shown != (c.want != "") {
			t.Errorf("%s: help visible = %v", c.game, shown)
		}
		if c.want != "" && !strings.Contains(f.panel.helpText.Text, c.want) {
			t.Errorf("%s: help = %q, want it to mention %q", c.game, f.panel.helpText.Text, c.want)
		}
	}
}

func TestPortApply(t *testing.T) {
	f := newFixture(t)
	// OnPort runs off the UI thread, so share state with it under a lock.
	var mu sync.Mutex
	var asked []int
	fail := false
	f.panel.OnPort = func(port int) error {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, port)
		if fail {
			return errors.New("port in use")
		}
		return nil
	}
	calls := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(asked)
	}

	for _, bad := range []string{"", "abc", "0", "70000", "47600"} {
		f.panel.bcast.entry.SetText(bad)
		if !f.panel.bcast.apply.Disabled() {
			t.Errorf("Apply enabled for %q", bad)
		}
	}

	f.panel.bcast.entry.SetText("48000")
	if f.panel.bcast.apply.Disabled() {
		t.Fatal("Apply disabled for a valid new port")
	}
	test.Tap(f.panel.bcast.apply)
	eventually(t, f, func() bool { return f.panel.port == 48000 })
	if !strings.Contains(f.panel.meta.Text, "ws://127.0.0.1:48000/ws") {
		t.Fatalf("meta = %q", f.panel.meta.Text)
	}
	if !f.panel.bcast.apply.Disabled() {
		t.Fatal("Apply should be disabled once the port is applied")
	}

	mu.Lock()
	fail = true
	mu.Unlock()
	f.panel.bcast.entry.SetText("49000")
	test.Tap(f.panel.bcast.apply)
	eventually(t, f, func() bool { return calls() == 2 && f.panel.bcast.entry.Text == "48000" })
	if f.panel.port != 48000 {
		t.Fatalf("port = %d after a failed move, want 48000", f.panel.port)
	}
}

func TestGamePortBox(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var got []string
	f.panel.OnGamePort = func(game string, port int) error {
		mu.Lock()
		got = append(got, fmt.Sprintf("%s=%d", game, port))
		mu.Unlock()
		return f.ctrl.SetPort(game, port)
	}

	f.ctrl.Select(adapter.LoL)
	f.panel.Refresh()
	if f.panel.gamePort.row.Visible() {
		t.Fatal("League's port is fixed; no box expected")
	}

	f.ctrl.Select(adapter.RL)
	f.panel.Refresh()
	if !f.panel.gamePort.row.Visible() || f.panel.gamePort.entry.Text != "49124" {
		t.Fatalf("rl box: visible=%v text=%q", f.panel.gamePort.row.Visible(), f.panel.gamePort.entry.Text)
	}
	f.panel.gamePort.entry.SetText("50124")
	test.Tap(f.panel.gamePort.apply)
	eventually(t, f, func() bool { return f.ctrl.Port(adapter.RL) == 50124 })
	if !strings.Contains(f.panel.helpText.Text, "port 50124") {
		t.Fatalf("help not updated: %q", f.panel.helpText.Text)
	}

	// Switching games shows that game's own port.
	f.ctrl.Select(adapter.Apex)
	f.panel.Refresh()
	if f.panel.gamePort.entry.Text != "7777" {
		t.Fatalf("apex box shows %q", f.panel.gamePort.entry.Text)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "rl=50124" {
		t.Fatalf("OnGamePort calls: %v", got)
	}
}

func TestNoPortDisablesGameControls(t *testing.T) {
	f := newFixture(t)
	f.ctrl.Select(adapter.SC2)
	f.panel.SetPort(0)
	if !f.panel.game.Disabled() || !f.panel.power.Disabled() {
		t.Fatal("game controls should be inert without a broadcast port")
	}
	if f.panel.bcast.entry.Disabled() {
		t.Fatal("the port box must stay usable to fix it")
	}
	f.panel.SetPort(48000)
	if f.panel.game.Disabled() || f.panel.power.Disabled() {
		t.Fatal("game controls should work again once a port is open")
	}
}

func TestErrorPaneCapsRows(t *testing.T) {
	f := newFixture(t)
	for i := range maxShownErrors + 3 {
		f.errs.Add(time.Now(), string(rune('a'+i)))
	}
	eventually(t, f, func() bool { return f.panel.errPane.Visible() })
	if n := len(f.panel.errList.Objects); n != maxShownErrors {
		t.Fatalf("%d rows, want %d", n, maxShownErrors)
	}
}

func TestThemeToggle(t *testing.T) {
	f := newFixture(t)
	var remembered *bool
	f.panel.OnTheme = func(dark bool) { remembered = &dark }
	test.Tap(f.panel.themeBtn)
	if f.panel.dark || remembered == nil || *remembered {
		t.Fatal("expected a switch to light, remembered")
	}
}

// TestScreenshots writes the panel's look in each state to the directory in
// PANEL_SHOTS, for eyeballing changes. It is skipped otherwise.
func TestScreenshots(t *testing.T) {
	dir := os.Getenv("PANEL_SHOTS")
	if dir == "" {
		t.Skip("set PANEL_SHOTS to write screenshots")
	}
	f := newFixture(t)
	shot := func(name string) {
		f.panel.Refresh()
		img := f.win.Canvas().Capture()
		out, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		png.Encode(out, img)
	}

	shot("1-fresh")
	f.ctrl.Select(adapter.SC2)
	f.ctrl.Start()
	shot("2-waiting")
	f.ctrl.Stop()
	f.errs.Add(time.Now(), "Counter-Strike 2 stopped: port 47601 is already in use by another program")
	f.errs.Add(time.Now(), "rejected game state: payload is not JSON")
	shot("3-errors")
	test.Tap(f.panel.themeBtn)
	shot("4-errors-light")
	test.Tap(f.panel.themeBtn)
	f.errs.Clear()
	f.ctrl.Select(adapter.Dota2)
	f.panel.Refresh() // let the window shrink back, then capture
	time.Sleep(resizeSettle + 50*time.Millisecond)
	shot("5-dota")
	f.ctrl.Select(adapter.RL)
	shot("6-rl")
}
