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
const appID = "com.singlestudio.gamestate"

type window struct {
	ctrl    *control.Controller
	errs    *control.ErrorLog
	hub     *relay.Hub
	log     *slog.Logger
	relay   *relayServer
	bind    string
	gsiURL  string
	dark    bool
	onTheme func(dark bool)
	onPort  func(port int) error
	show    *atomic.Pointer[func()]
}

// runWindow shows GameState's window and blocks until it is closed or
// ctx is cancelled. Closing the window quits GameState.
func runWindow(ctx context.Context, w window) {
	a := app.NewWithID(appID)
	a.SetIcon(fyne.NewStaticResource("Icon.png", iconPNG))
	win := a.NewWindow(ui.Title)
	win.SetMaster()

	p := ui.NewPanel(a, win, w.ctrl, w.errs, w.hub, ui.Options{
		Bind:   w.bind,
		Port:   w.relay.Port(),
		GSIURL: w.gsiURL,
		Dark:   w.dark,
	})
	p.OnTheme = w.onTheme
	p.OnPort = w.onPort
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
		for {
			select {
			case <-ctx.Done():
				fyne.Do(a.Quit)
				return
			case err := <-w.relay.errc:
				w.log.Error("the broadcast port stopped", "err", err)
				fyne.Do(func() { p.SetPort(0) })
			}
		}
	}()

	p.Show()
	a.Run()
}
