// Package ui is GameState's window: an on/off switch, a game picker and
// an error pane that appears only when something has gone wrong.
package ui

import (
	"context"
	"fmt"
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

// Panel is GameState's window content.
type Panel struct {
	ctrl     *control.Controller
	errs     *control.ErrorLog
	hub      *relay.Hub
	relayURL string
	app      fyne.App
	win      fyne.Window

	// OnTheme is called when the user switches theme, so it can be remembered.
	OnTheme func(dark bool)

	// do runs a function on the UI thread; tests swap it for a queue.
	do func(func())

	dark     bool
	disabled bool
	updating bool

	game       *widget.Select
	power      *Switch
	powerLabel *widget.Label
	status     *widget.Label
	meta       *widget.Label
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

// NewPanel builds the window content and sets it on win.
func NewPanel(a fyne.App, win fyne.Window, ctrl *control.Controller, errs *control.ErrorLog, hub *relay.Hub, relayURL string, dark bool) *Panel {
	p := &Panel{ctrl: ctrl, errs: errs, hub: hub, relayURL: relayURL, app: a, win: win, dark: dark, do: fyne.Do}

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

	p.powerLabel = widget.NewLabelWithStyle("Off", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.status = widget.NewLabel("Not relaying")
	p.power = NewSwitch(p.onPower)

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
		container.NewBorder(nil, nil, nil, p.power, container.NewVBox(p.powerLabel, p.status)),
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

// Disable makes the controls inert, for when the relay itself could not
// start. The error pane still shows why.
func (p *Panel) Disable() {
	p.disabled = true
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
	if p.disabled {
		p.game.Disable()
	}

	p.power.SetOn(st.Running)
	if st.Game == "" || p.disabled {
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
	p.meta.SetText(fmt.Sprintf("Single Studio connects to %s · %s connected", p.relayURL, overlays))

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
