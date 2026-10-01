// Package ui is GameState's window: an on/off switch, a game picker and
// an error pane that appears only when something has gone wrong.
package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/fourcourtjester/single-studio-gamestate/internal/adapter"
	"github.com/fourcourtjester/single-studio-gamestate/internal/control"
	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
)

const (
	// Title heads the window and its title bar.
	Title = "Single Studio - GameState"
	// Width is the window's starting width.
	Width = 400
	// maxShownErrors caps the pane; repeats already fold into one line.
	maxShownErrors = 5
	// resizeSettle is how long the OS gets to apply a resize before the
	// window's height is trusted.
	resizeSettle = 300 * time.Millisecond
)

// Setup guides for games whose feed the user has to switch on themselves.
// Valve's GSI guide was written for CS:GO, but CS2 and Dota 2 set up their
// GSI files the same way.
var (
	gsiGuide, _  = url.Parse("https://developer.valvesoftware.com/wiki/Counter-Strike:_Global_Offensive_Game_State_Integration")
	rlGuide, _   = url.Parse("https://www.rocketleague.com/en/developer/stats-api")
	apexGuide, _ = url.Parse("https://apexliveapi.com/")
)

// help is the setup hint shown under the game picker for one game. url is
// optional.
type help struct {
	text string
	link string
	url  *url.URL
}

// helpFor returns the setup hint for game, given the port it uses and the
// address GameState listens on. Games that need no setup have none.
func helpFor(game, bind string, port int) (help, bool) {
	p := strconv.Itoa(port)
	switch game {
	case adapter.CS2, adapter.Dota2:
		return help{"Your Game State Integration file should send to http://" + net.JoinHostPort(bind, p) + "/",
			"Setup guide (Valve)", gsiGuide}, true
	case adapter.Apex:
		return help{`Launch Apex with +cl_liveapi_enabled 1 +cl_liveapi_ws_servers "ws://` + net.JoinHostPort(bind, p) + `"`,
			"LiveAPI guide", apexGuide}, true
	case adapter.RL:
		return help{"Turn on Rocket League's Stats API (PacketSendRate in DefaultStatsAPI.ini). GameState connects to its WebSocket (WebPort) on port " + p,
			"Stats API guide (Psyonix)", rlGuide}, true
	case adapter.SC2:
		return help{"GameState reads StarCraft II's client API on port " + p + ". Match the -clientapi option if you start the game with one.", "", nil}, true
	}
	return help{}, false
}

// Panel is GameState's window content.
type Panel struct {
	ctrl *control.Controller
	errs *control.ErrorLog
	hub  *relay.Hub
	bind string
	port int // broadcast port being served; 0 when none
	app  fyne.App
	win  fyne.Window

	// OnTheme is called when the user switches theme, so it can be remembered.
	OnTheme func(dark bool)

	// OnPort moves the broadcast port. It reports failures itself (they
	// land in the error pane) and returns them so the panel can revert.
	OnPort func(port int) error

	// OnGamePort changes a game's port, reporting failures the same way.
	OnGamePort func(game string, port int) error

	// do runs a function on the UI thread; tests swap it for a queue.
	do func(func())

	dark     bool
	updating bool

	game       *widget.Select
	power      *Switch
	powerLabel *widget.Label
	status     *widget.Label
	meta       *widget.Label
	bcast      *portField // broadcast port
	gamePort   *portField // selected game's port, for games where it can change
	shownGame  string     // game the game-port box was last filled for
	helpBox    *fyne.Container
	helpText   *widget.Label
	helpLink   *widget.Hyperlink
	themeBtn   *widget.Button
	errPane    *fyne.Container
	errBG      *canvas.Rectangle
	errList    *fyne.Container
	shownErrs  string

	// The window's real height can differ from what was asked for, and only
	// settles after the OS applies a resize. settled is the height observed
	// once our last resize took effect; any other height means the user
	// resized the window, and it is left alone.
	resizedAt time.Time
	settled   float32
}

// Options configures a Panel.
type Options struct {
	Bind string // address the broadcast port is on
	Port int    // broadcast port Single Studio connects to; 0 if it couldn't open
	Dark bool
}

// NewPanel builds the window content and sets it on win.
func NewPanel(a fyne.App, win fyne.Window, ctrl *control.Controller, errs *control.ErrorLog, hub *relay.Hub, o Options) *Panel {
	p := &Panel{ctrl: ctrl, errs: errs, hub: hub, bind: o.Bind, port: o.Port, app: a, win: win, dark: o.Dark, do: fyne.Do}
	uiThread := func(f func()) { p.do(f) } // p.do may be swapped after construction
	// Set the theme before building widgets so their first frame uses it.
	a.Settings().SetTheme(newTheme(o.Dark))

	title := widget.NewLabelWithStyle(Title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.themeBtn = widget.NewButtonWithIcon("", nil, p.toggleTheme)
	p.themeBtn.Importance = widget.LowImportance

	gameCaption := widget.NewLabel("Game")
	gameCaption.SizeName = theme.SizeNameCaptionText
	var names []string
	for _, t := range adapter.Titles {
		if t.Available {
			names = append(names, t.Name)
		}
	}
	p.game = widget.NewSelect(names, p.onSelect)
	p.game.PlaceHolder = "Choose a game…"

	// Some games only send data once the user has set them up (a GSI file,
	// an ini setting). That setup is theirs to do; say what GameState expects and
	// link to the game's own guide.
	p.helpText = widget.NewLabel("")
	p.helpText.Wrapping = fyne.TextWrapWord
	p.helpText.SizeName = theme.SizeNameCaptionText
	p.helpText.Importance = widget.LowImportance
	p.helpLink = widget.NewHyperlink("", nil)
	p.helpLink.SizeName = theme.SizeNameCaptionText
	p.helpBox = container.NewVBox(p.helpText, p.helpLink)
	p.helpBox.Hide()

	p.powerLabel = widget.NewLabelWithStyle("Off", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.status = widget.NewLabel("Not relaying")
	p.power = NewSwitch(p.onPower)

	// The game's own port, for games where the user may have changed it.
	p.gamePort = newPortField("Game port", uiThread)
	p.gamePort.onApply = func(port int) error {
		if p.OnGamePort == nil {
			return errors.New("game port can't be changed")
		}
		return p.OnGamePort(p.ctrl.State().Game, port)
	}
	p.gamePort.applied = func(int) { p.Refresh() }
	p.gamePort.row.Hide()

	// The port Single Studio connects to.
	p.bcast = newPortField("Broadcast port", uiThread)
	p.bcast.onApply = func(port int) error {
		if p.OnPort == nil {
			return errors.New("broadcast port can't be changed")
		}
		return p.OnPort(port)
	}
	p.bcast.applied = func(port int) {
		p.port = port
		p.Refresh()
	}
	p.bcast.Set(o.Port)

	p.meta = widget.NewLabel("")
	p.meta.Wrapping = fyne.TextWrapWord
	p.meta.Importance = widget.LowImportance
	p.meta.SizeName = theme.SizeNameCaptionText

	errTitle := widget.NewLabelWithStyle("Errors", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	errTitle.Importance = widget.DangerImportance
	clear := widget.NewButton("Clear", func() {
		p.errs.Clear()
		p.Refresh()
	})
	clear.Importance = widget.LowImportance
	p.errList = container.NewVBox()
	p.errBG = canvas.NewRectangle(nil)
	p.errBG.CornerRadius = 6
	p.errBG.StrokeWidth = 1
	p.errPane = container.NewStack(p.errBG, container.NewPadded(
		container.NewBorder(container.NewBorder(nil, nil, errTitle, clear), nil, nil, nil, p.errList)))
	p.errPane.Hide()

	win.SetContent(container.NewPadded(container.NewVBox(
		container.NewBorder(nil, nil, nil, p.themeBtn, title),
		container.NewVBox(gameCaption, p.game),
		p.helpBox,
		p.gamePort.row,
		container.NewBorder(nil, nil, nil, p.power, container.NewVBox(p.powerLabel, p.status)),
		p.bcast.row,
		p.meta,
		p.errPane,
	)))
	p.applyTheme()
	p.Refresh()
	return p
}

// Show sizes the window to its content and shows it.
func (p *Panel) Show() {
	// Wrapped text only knows its height once it has a width, so size
	// twice: once to lay out at Width, once to fit what that produced.
	for range 2 {
		p.win.Resize(fyne.NewSize(Width, p.win.Content().MinSize().Height))
	}
	p.resizedAt = time.Now()
	p.win.Show()
}

// Run refreshes the panel once a second until ctx is cancelled.
func (p *Panel) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.do(p.Refresh)
		}
	}
}

// SetPort records the broadcast port being served, 0 when none is. With no
// port the game controls are inert until the user picks one that works.
func (p *Panel) SetPort(port int) {
	p.port = port
	p.bcast.Set(port)
	p.Refresh()
}

// Refresh redraws the panel from the controller's state. It must run on the
// UI thread.
func (p *Panel) Refresh() {
	st := p.ctrl.State()

	p.updating = true
	if t, ok := adapter.Lookup(st.Game); ok && t.Available {
		p.game.SetSelected(t.Name)
	} else {
		p.game.ClearSelected()
	}
	p.updating = false
	gamePort := p.ctrl.Port(st.Game)
	if h, ok := helpFor(st.Game, p.bind, gamePort); ok {
		p.helpText.SetText(h.text)
		p.helpLink.SetText(h.link)
		p.helpLink.SetURL(h.url)
		if h.url != nil {
			p.helpLink.Show()
		} else {
			p.helpLink.Hide()
		}
		p.helpBox.Show()
	} else {
		p.helpBox.Hide()
	}
	if gamePort != 0 {
		// Refill the box only when the game changes, so typing isn't lost.
		if st.Game != p.shownGame {
			p.gamePort.Set(gamePort)
		}
		p.gamePort.row.Show()
	} else {
		p.gamePort.row.Hide()
	}
	p.shownGame = st.Game
	broadcasting := p.port != 0
	if broadcasting {
		p.game.Enable()
	} else {
		p.game.Disable()
	}

	p.power.SetOn(st.Running)
	if st.Game == "" || !broadcasting {
		p.power.Disable()
	} else {
		p.power.Enable()
	}
	p.powerLabel.SetText(map[bool]string{true: "On", false: "Off"}[st.Running])

	switch {
	case !st.Running:
		p.status.Importance = widget.LowImportance
		p.status.SetText("Not relaying")
	case st.LastData != nil:
		p.status.Importance = widget.SuccessImportance
		p.status.SetText("Receiving data · updated " + ago(*st.LastData))
	default:
		t, _ := adapter.Lookup(st.Game)
		p.status.Importance = widget.LowImportance
		p.status.SetText("Waiting for " + t.Name + "…")
	}

	overlays := fmt.Sprintf("%d overlays", p.hub.Stats().Clients)
	if p.hub.Stats().Clients == 1 {
		overlays = "1 overlay"
	}
	if broadcasting {
		url := "ws://" + net.JoinHostPort(p.bind, strconv.Itoa(p.port)) + "/ws"
		p.meta.SetText(fmt.Sprintf("Single Studio connects to %s · %s connected", url, overlays))
	} else {
		p.meta.SetText("Not broadcasting. Choose a free port and press Apply.")
	}

	p.refreshErrors()
	p.fit()
}

func (p *Panel) refreshErrors() {
	entries := p.errs.Entries()
	if len(entries) > maxShownErrors {
		entries = entries[:maxShownErrors]
	}
	var key strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&key, "%d|%d|%s\n", e.Time.UnixNano(), e.Count, e.Message)
	}
	if key.String() == p.shownErrs {
		return
	}
	p.shownErrs = key.String()

	p.errList.RemoveAll()
	for _, e := range entries {
		text := e.Time.Format("3:04:05 PM") + "   " + e.Message
		if e.Count > 1 {
			text += fmt.Sprintf("  ×%d", e.Count)
		}
		l := widget.NewLabel(text)
		l.Wrapping = fyne.TextWrapWord
		l.Importance = widget.DangerImportance
		p.errList.Add(l)
	}
	if len(entries) == 0 {
		p.errPane.Hide()
	} else {
		p.errPane.Show()
	}
}

// fit grows the window when the error pane needs room, and shrinks it back
// once the pane closes, unless the user has resized the window themselves.
//
// Wrapped text only reports its true height once laid out at the window's
// width, so a resize is followed by a second measurement.
func (p *Panel) fit() {
	if !p.resizedAt.IsZero() && time.Since(p.resizedAt) > resizeSettle {
		p.settled = p.win.Canvas().Size().Height
		p.resizedAt = time.Time{}
	}
	for range 2 {
		want := p.win.Content().MinSize().Height
		cur := p.win.Canvas().Size()
		grow := want > cur.Height+0.5
		ours := p.resizedAt.IsZero() && abs(cur.Height-p.settled) < 1
		shrink := want < cur.Height-1 && ours
		if !grow && !shrink {
			return
		}
		p.win.Resize(fyne.NewSize(cur.Width, want))
		p.resizedAt = time.Now()
	}
}

func (p *Panel) onSelect(name string) {
	if p.updating {
		return
	}
	for _, t := range adapter.Titles {
		if t.Name == name {
			// Switching games restarts the adapter, which can take a moment
			// to release its ports; keep that off the UI thread.
			go func() {
				p.ctrl.Select(t.ID)
				p.do(p.Refresh)
			}()
			return
		}
	}
}

func (p *Panel) onPower(on bool) {
	go func() {
		if on {
			p.ctrl.Start()
		} else {
			p.ctrl.Stop()
		}
		p.do(p.Refresh)
	}()
}

func (p *Panel) toggleTheme() {
	p.dark = !p.dark
	p.applyTheme()
	if p.OnTheme != nil {
		p.OnTheme(p.dark)
	}
}

func (p *Panel) applyTheme() {
	p.app.Settings().SetTheme(newTheme(p.dark))
	if p.dark {
		p.themeBtn.SetIcon(moonIcon)
	} else {
		p.themeBtn.SetIcon(sunIcon)
	}
	p.errBG.FillColor, p.errBG.StrokeColor = errorColors(p.dark)
	p.errBG.Refresh()
}

func ago(t time.Time) string {
	s := int(time.Since(t).Round(time.Second).Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds ago", max(s, 0))
	}
	return fmt.Sprintf("%dm ago", s/60)
}

func abs(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}
