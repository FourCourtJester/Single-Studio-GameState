package ui

import (
	"errors"
	"fmt"
	"image/color"
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
	f.panel.game.choose(adapter.CS2) // as if picked from the open list
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
	if !strings.Contains(f.panel.meta.Text, "ws://127.0.0.1:48000 ") {
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
	eventually(t, f, func() bool {
		return f.ctrl.Port(adapter.RL) == 50124 && strings.Contains(f.panel.helpText.Text, "port 50124")
	})

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

func TestGamePickerList(t *testing.T) {
	f := newFixture(t)
	if f.panel.game.Selected != "" {
		t.Fatalf("selected %q before picking", f.panel.game.Selected)
	}
	test.Tap(f.panel.game)
	if !f.panel.game.isOpen() {
		t.Fatal("list did not open")
	}
	var names []string
	for _, r := range f.panel.game.list.rows {
		names = append(names, r.title.Name)
	}
	if len(names) != 6 || names[0] != "Apex Legends" {
		t.Fatalf("rows: %v (War3 should be left out)", names)
	}
	test.Tap(f.panel.game.list.rows[4]) // Rocket League
	eventually(t, f, func() bool { return f.ctrl.State().Game == adapter.RL })
	if f.panel.game.isOpen() {
		t.Fatal("list should close after picking")
	}
}

func TestOpenListFitsInWindow(t *testing.T) {
	f := newFixture(t)
	// Shorten the panel so the open list can't fit inside it as it stands.
	f.panel.bcast.row.Hide()
	f.panel.meta.Hide()
	f.panel.Show()
	before := f.win.Canvas().Size().Height
	test.Tap(f.panel.game)
	popup := f.panel.game.popup
	bottom := popup.Content.Position().Y + popup.Content.Size().Height
	if canvasH := f.win.Canvas().Size().Height; bottom > canvasH {
		t.Fatalf("list ends at %v but the window is %v tall", bottom, canvasH)
	}
	if f.win.Canvas().Size().Height <= before {
		t.Fatal("window should grow to fit the open list")
	}

	f.panel.game.list.cancel()
	time.Sleep(resizeSettle + 50*time.Millisecond)
	eventually(t, f, func() bool { return abs(f.win.Canvas().Size().Height-before) < 1 })
}

func TestGamePickerKeyboard(t *testing.T) {
	f := newFixture(t)
	g := f.panel.game
	key := func(target fyne.Focusable, name fyne.KeyName) { target.TypedKey(&fyne.KeyEvent{Name: name}) }
	game := func() string {
		var id string
		eventually(t, f, func() bool { id = f.ctrl.State().Game; return id == g.Selected })
		return id
	}

	f.win.Canvas().Focus(g)
	key(g, fyne.KeyDown) // nothing selected yet: Down picks the first game
	if got := game(); got != adapter.Apex {
		t.Fatalf("after Down: %q", got)
	}
	key(g, fyne.KeyDown)
	if got := game(); got != adapter.CS2 {
		t.Fatalf("after Down again: %q", got)
	}
	key(g, fyne.KeyUp)
	key(g, fyne.KeyUp) // stops at the top
	if got := game(); got != adapter.Apex {
		t.Fatalf("after Up twice: %q", got)
	}
	g.TypedRune('s')
	if got := game(); got != adapter.SC2 {
		t.Fatalf("after typing s: %q", got)
	}
	key(g, fyne.KeyHome)
	if got := game(); got != adapter.Apex {
		t.Fatalf("after Home: %q", got)
	}

	// Enter opens the list and focuses it; Escape closes without changing.
	key(g, fyne.KeyReturn)
	if !g.isOpen() || f.win.Canvas().Focused() != g.list {
		t.Fatal("Enter should open the list and focus it")
	}
	l := g.list
	key(l, fyne.KeyDown)
	key(l, fyne.KeyEscape)
	if g.isOpen() || g.Selected != adapter.Apex || f.win.Canvas().Focused() != g {
		t.Fatalf("Escape: open=%v selected=%q", g.isOpen(), g.Selected)
	}

	// Space opens; Down, Down, Enter picks the third game.
	g.TypedRune(' ')
	l = g.list
	key(l, fyne.KeyDown)
	key(l, fyne.KeyDown)
	key(l, fyne.KeyReturn)
	if got := game(); got != adapter.Dota2 || g.isOpen() {
		t.Fatalf("after picking from the list: %q open=%v", got, g.isOpen())
	}
}

func TestBadgeTextReadable(t *testing.T) {
	for game, b := range badges {
		fg := readableOn(b.color)
		lum := 0.299*float64(b.color.R) + 0.587*float64(b.color.G) + 0.114*float64(b.color.B)
		if (lum > 150) != (fg != color.White) {
			t.Errorf("%s: text colour doesn't suit its badge", game)
		}
	}
}

func TestWindowAboveMinimumIsNotFought(t *testing.T) {
	// Recreate a real window that stops short of the height asked for (an
	// OS minimum), then the user dragging it taller. Neither should make
	// the panel keep resizing.
	f := newFixture(t)
	want := f.win.Content().MinSize().Height
	f.panel.requested = want
	f.win.Resize(fyne.NewSize(Width, want+9)) // the OS's minimum, above what was asked
	f.panel.resizedAt = time.Now().Add(-time.Second)
	f.panel.Refresh() // records the settled height
	f.panel.Refresh()
	if h := f.win.Canvas().Size().Height; h != want+9 {
		t.Fatalf("panel kept fighting the window's minimum: height %v", h)
	}

	f.win.Resize(fyne.NewSize(Width, 650)) // the user enlarges it
	for range 3 {
		time.Sleep(resizeSettle)
		f.panel.Refresh()
	}
	if h := f.win.Canvas().Size().Height; h != 650 {
		t.Fatalf("window snapped back to %v after the user enlarged it", h)
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
	f.panel.Refresh()
	test.Tap(f.panel.game)
	img := f.win.Canvas().Capture() // capture the open list as is
	out, _ := os.Create(filepath.Join(dir, "7-picker-open.png"))
	png.Encode(out, img)
	out.Close()
}
