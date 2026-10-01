package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/fourcourtjester/single-studio-gamestate/internal/adapter"
)

// badges give each game a short label and colour, used in place of logos.
var badges = map[string]struct {
	text  string
	color color.NRGBA
}{
	adapter.Apex:  {"APEX", color.NRGBA{0xe2, 0x48, 0x3d, 0xff}},
	adapter.CS2:   {"CS2", color.NRGBA{0xe8, 0xa3, 0x3d, 0xff}},
	adapter.Dota2: {"DOTA", color.NRGBA{0x9e, 0x2a, 0x2b, 0xff}},
	adapter.LoL:   {"LOL", color.NRGBA{0x0a, 0xc8, 0xb9, 0xff}},
	adapter.RL:    {"RL", color.NRGBA{0x1e, 0x88, 0xe5, 0xff}},
	adapter.SC2:   {"SC2", color.NRGBA{0x7b, 0x5c, 0xd6, 0xff}},
	adapter.War3:  {"WC3", color.NRGBA{0x8d, 0x6e, 0x3f, 0xff}},
}

const (
	badgeW = 44
	badgeH = 22
)

// newBadge draws a game's badge: its short label on its colour, with dark
// or light text, whichever reads better on that colour. An unknown game
// gets an empty grey badge.
func newBadge(game string) fyne.CanvasObject {
	b, ok := badges[game]
	bg := color.NRGBA{0x52, 0x52, 0x5b, 0xff}
	if ok {
		bg = b.color
	}
	rect := canvas.NewRectangle(bg)
	rect.CornerRadius = 4
	rect.SetMinSize(fyne.NewSize(badgeW, badgeH))

	text := canvas.NewText(b.text, readableOn(bg))
	text.TextStyle = fyne.TextStyle{Bold: true}
	text.TextSize = 11
	text.Alignment = fyne.TextAlignCenter
	return container.NewStack(rect, container.NewCenter(text))
}

// readableOn picks black or white text for a background, by luminance.
func readableOn(c color.NRGBA) color.Color {
	lum := 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
	if lum > 150 {
		return color.NRGBA{0x11, 0x11, 0x14, 0xff}
	}
	return color.White
}

// GamePicker is a drop-down of games, each shown with its badge.
type GamePicker struct {
	widget.DisableableWidget
	Selected    string // game namespace, "" for none
	Placeholder string
	OnChanged   func(game string)

	games []adapter.Title
	popup *widget.PopUp
}

// NewGamePicker lists the available titles.
func NewGamePicker(changed func(game string)) *GamePicker {
	p := &GamePicker{OnChanged: changed, Placeholder: "Choose a game…"}
	for _, t := range adapter.Titles {
		if t.Available {
			p.games = append(p.games, t)
		}
	}
	p.ExtendBaseWidget(p)
	return p
}

// SetSelected shows game as selected without calling OnChanged.
func (p *GamePicker) SetSelected(game string) {
	if p.Selected == game {
		return
	}
	p.Selected = game
	p.Refresh()
}

// Tapped opens the list under the picker.
func (p *GamePicker) Tapped(*fyne.PointEvent) {
	if p.Disabled() {
		return
	}
	c := fyne.CurrentApp().Driver().CanvasForObject(p)
	if c == nil {
		return
	}
	rows := container.NewVBox()
	for _, t := range p.games {
		rows.Add(newPickerRow(t, t.ID == p.Selected, p.choose))
	}
	p.popup = widget.NewPopUp(rows, c)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(p)
	p.popup.ShowAtPosition(pos.Add(fyne.NewPos(0, p.Size().Height)))
	p.popup.Resize(fyne.NewSize(p.Size().Width, rows.MinSize().Height))
}

func (p *GamePicker) choose(game string) {
	if p.popup != nil {
		p.popup.Hide()
		p.popup = nil
	}
	if game == p.Selected {
		return
	}
	p.Selected = game
	p.Refresh()
	if p.OnChanged != nil {
		p.OnChanged(game)
	}
}

// Cursor shows a pointer over the picker.
func (p *GamePicker) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (p *GamePicker) CreateRenderer() fyne.WidgetRenderer {
	r := &pickerRenderer{p: p, bg: canvas.NewRectangle(nil), chevron: widget.NewIcon(theme.MenuDropDownIcon())}
	r.bg.CornerRadius = theme.InputRadiusSize()
	r.label = widget.NewLabel("")
	r.Refresh()
	return r
}

type pickerRenderer struct {
	p       *GamePicker
	bg      *canvas.Rectangle
	badge   fyne.CanvasObject
	label   *widget.Label
	chevron *widget.Icon
	shown   string // game the badge was built for
}

func (r *pickerRenderer) MinSize() fyne.Size {
	pad := theme.Padding()
	h := max(r.label.MinSize().Height, badgeH+2*pad)
	return fyne.NewSize(badgeW+r.label.MinSize().Width+theme.IconInlineSize()+4*pad, h)
}

func (r *pickerRenderer) Layout(size fyne.Size) {
	pad := theme.Padding()
	r.bg.Resize(size)
	x := float32(0)
	if r.badge != nil {
		x = 2 * pad
		r.badge.Move(fyne.NewPos(x, (size.Height-badgeH)/2))
		r.badge.Resize(fyne.NewSize(badgeW, badgeH))
		x += badgeW
	}
	icon := theme.IconInlineSize()
	r.chevron.Move(fyne.NewPos(size.Width-icon-2*pad, (size.Height-icon)/2))
	r.chevron.Resize(fyne.NewSize(icon, icon))
	r.label.Move(fyne.NewPos(x, 0))
	r.label.Resize(fyne.NewSize(size.Width-x-icon-2*pad, size.Height))
}

func (r *pickerRenderer) Refresh() {
	r.bg.FillColor = theme.Color(theme.ColorNameInputBackground)
	r.bg.Refresh()
	if r.shown != r.p.Selected || r.badge == nil && r.p.Selected != "" {
		r.badge = nil
		if r.p.Selected != "" {
			r.badge = newBadge(r.p.Selected)
		}
		r.shown = r.p.Selected
	}
	if t, ok := adapter.Lookup(r.p.Selected); ok {
		r.label.SetText(t.Name)
		r.label.Importance = widget.MediumImportance
	} else {
		r.label.SetText(r.p.Placeholder)
		r.label.Importance = widget.LowImportance
	}
	if r.p.Disabled() {
		r.label.Importance = widget.LowImportance
	}
	r.label.Refresh()
	r.Layout(r.p.Size())
	canvas.Refresh(r.p)
}

func (r *pickerRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.bg, r.label, r.chevron}
	if r.badge != nil {
		objs = append(objs, r.badge)
	}
	return objs
}

func (r *pickerRenderer) Destroy() {}

// pickerRow is one game in the open list.
type pickerRow struct {
	widget.BaseWidget
	title    adapter.Title
	selected bool
	hovered  bool
	choose   func(game string)
}

func newPickerRow(t adapter.Title, selected bool, choose func(string)) *pickerRow {
	r := &pickerRow{title: t, selected: selected, choose: choose}
	r.ExtendBaseWidget(r)
	return r
}

func (r *pickerRow) Tapped(*fyne.PointEvent)        { r.choose(r.title.ID) }
func (r *pickerRow) Cursor() desktop.Cursor         { return desktop.PointerCursor }
func (r *pickerRow) MouseIn(*desktop.MouseEvent)    { r.hovered = true; r.Refresh() }
func (r *pickerRow) MouseMoved(*desktop.MouseEvent) {}
func (r *pickerRow) MouseOut()                      { r.hovered = false; r.Refresh() }

func (r *pickerRow) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(nil)
	bg.CornerRadius = theme.InputRadiusSize()
	label := widget.NewLabel(r.title.Name)
	if r.selected {
		label.TextStyle = fyne.TextStyle{Bold: true}
	}
	content := container.NewBorder(nil, nil, container.NewCenter(newBadge(r.title.ID)), nil, label)
	row := &rowRenderer{r: r, bg: bg, content: container.NewPadded(content)}
	row.Refresh()
	return row
}

type rowRenderer struct {
	r       *pickerRow
	bg      *canvas.Rectangle
	content *fyne.Container
}

func (rr *rowRenderer) MinSize() fyne.Size { return rr.content.MinSize() }

func (rr *rowRenderer) Layout(size fyne.Size) {
	rr.bg.Resize(size)
	rr.content.Resize(size)
}

func (rr *rowRenderer) Refresh() {
	rr.bg.FillColor = color.Transparent
	if rr.r.hovered {
		rr.bg.FillColor = theme.Color(theme.ColorNameHover)
	}
	rr.bg.Refresh()
}

func (rr *rowRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{rr.bg, rr.content} }
func (rr *rowRenderer) Destroy()                     {}
