package main

import (
	"context"
	_ "embed"
	"log/slog"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/fourcourtjester/single-studio-gamestate/internal/control"
	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
	"github.com/fourcourtjester/single-studio-gamestate/internal/ui"
)

//go:embed Icon.png
var iconPNG []byte

// appID identifies the app to the OS; it must match FyneApp.toml.
const appID = "com.singlestudio.companion"

type window struct {
	ctrl     *control.Controller
	errs     *control.ErrorLog
	hub      *relay.Hub
	log      *slog.Logger
	relayURL string
	broken   bool // the relay couldn't start; the window only reports why
	dark     bool
	onTheme  func(dark bool)
	show     *atomic.Pointer[func()]
	errc     <-chan error
}

// runWindow shows the companion's window and blocks until it is closed or
// ctx is cancelled. Closing the window quits the companion.
func runWindow(ctx context.Context, w window) {
	a := app.NewWithID(appID)
	a.SetIcon(fyne.NewStaticResource("Icon.png", iconPNG))
	win := a.NewWindow(ui.Title)
	win.SetMaster()

	p := ui.NewPanel(a, win, w.ctrl, w.errs, w.hub, w.relayURL, w.dark)
	p.OnTheme = w.onTheme
	if w.broken {
		p.Disable()
	}
	raise := func() {
		fyne.Do(func() {
			win.Show()
			win.RequestFocus()
		})
	}
	w.show.Store(&raise)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go p.Run(ctx)
	go func() {
		select {
		case <-ctx.Done():
			fyne.Do(a.Quit)
		case err := <-w.errc:
			w.log.Error("the relay stopped", "err", err)
			fyne.Do(p.Disable)
		}
	}()

	p.Show()
	a.Run()
}
