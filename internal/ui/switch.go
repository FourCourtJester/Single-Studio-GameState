package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	switchW = 52
	switchH = 30
	knobPad = 3
)

// Switch is an on/off toggle. Fyne has none built in.
type Switch struct {
	widget.DisableableWidget
	On        bool
	OnChanged func(on bool)
}

// NewSwitch returns an off switch that calls changed when the user flips it.
func NewSwitch(changed func(on bool)) *Switch {
	s := &Switch{OnChanged: changed}
	s.ExtendBaseWidget(s)
	return s
}

// SetOn changes the state without calling OnChanged.
func (s *Switch) SetOn(on bool) {
	if s.On == on {
		return
	}
	s.On = on
	s.Refresh()
}

// Tapped flips the switch.
func (s *Switch) Tapped(*fyne.PointEvent) {
	if s.Disabled() {
		return
	}
	s.On = !s.On
	s.Refresh()
	if s.OnChanged != nil {
		s.OnChanged(s.On)
	}
}

// Cursor shows a pointer over the switch.
func (s *Switch) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (s *Switch) CreateRenderer() fyne.WidgetRenderer {
	track := canvas.NewRectangle(color.Transparent)
	track.CornerRadius = switchH / 2
	knob := canvas.NewCircle(color.White)
	r := &switchRenderer{s: s, track: track, knob: knob}
	r.Refresh()
	return r
}

type switchRenderer struct {
	s     *Switch
	track *canvas.Rectangle
	knob  *canvas.Circle
}

func (r *switchRenderer) MinSize() fyne.Size { return fyne.NewSize(switchW, switchH) }

func (r *switchRenderer) Layout(size fyne.Size) {
	top := (size.Height - switchH) / 2
	left := size.Width - switchW
	r.track.Move(fyne.NewPos(left, top))
	r.track.Resize(fyne.NewSize(switchW, switchH))

	x := left + knobPad
	if r.s.On {
		x = left + switchW - switchH + knobPad
	}
	r.knob.Move(fyne.NewPos(x, top+knobPad))
	r.knob.Resize(fyne.NewSize(switchH-2*knobPad, switchH-2*knobPad))
}

func (r *switchRenderer) Refresh() {
	if r.s.On {
		r.track.FillColor = theme.Color(theme.ColorNamePrimary)
	} else {
		r.track.FillColor = theme.Color(theme.ColorNameInputBorder)
	}
	r.knob.FillColor = color.White
	if r.s.Disabled() {
		r.track.FillColor = theme.Color(theme.ColorNameDisabledButton)
		r.knob.FillColor = theme.Color(theme.ColorNameDisabled)
	}
	r.Layout(r.s.Size())
	r.track.Refresh()
	r.knob.Refresh()
}

func (r *switchRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.track, r.knob} }

func (r *switchRenderer) Destroy() {}
