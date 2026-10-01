package ui

import (
	"errors"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// portField is a labelled port box with an Apply button. Apply is enabled
// only for a valid port that differs from the one in use. Applying runs off
// the UI thread, since moving a port can wait on a server shutting down; if
// it fails the box goes back to the port still in use.
type portField struct {
	entry   *widget.Entry
	apply   *widget.Button
	row     *fyne.Container
	current int // port in use; 0 when none

	onApply func(port int) error // does the move; reports its own errors
	applied func(port int)       // runs on the UI thread after a move
	do      func(func())         // runs a function on the UI thread
}

func newPortField(label string, do func(func())) *portField {
	f := &portField{do: do}
	f.entry = widget.NewEntry()
	f.entry.Validator = func(s string) error {
		_, err := parsePort(s)
		return err
	}
	f.entry.OnChanged = func(string) { f.update() }
	f.entry.OnSubmitted = func(string) { f.submit() }
	f.apply = widget.NewButton("Apply", f.submit)
	f.row = container.NewBorder(nil, nil, widget.NewLabel(label), f.apply, f.entry)
	f.update()
	return f
}

// Set records the port in use and shows it.
func (f *portField) Set(port int) {
	f.current = port
	if port != 0 {
		f.entry.SetText(strconv.Itoa(port))
	}
	f.update()
}

func (f *portField) update() {
	if n, err := parsePort(f.entry.Text); err != nil || n == f.current {
		f.apply.Disable()
	} else {
		f.apply.Enable()
	}
}

func (f *portField) submit() {
	n, err := parsePort(f.entry.Text)
	if err != nil || n == f.current || f.onApply == nil {
		return
	}
	f.apply.Disable()
	go func() {
		err := f.onApply(n)
		f.do(func() {
			if err != nil {
				f.Set(f.current)
				return
			}
			f.Set(n)
			if f.applied != nil {
				f.applied(n)
			}
		})
	}()
}

func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, errors.New("enter a port from 1 to 65535")
	}
	return n, nil
}
