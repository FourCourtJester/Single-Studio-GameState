package ui

import (
	"image/color"
	"unicode"
	"unicode/utf8"

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

// GamePicker is a drop-down of games, each shown with its badge. From the
// keyboard it works like a native drop-down: when focused, Up and Down
// change the game, a letter jumps to the next game starting with it, and
// Enter or Space opens the list.
type GamePicker struct {
	widget.DisableableWidget
	Selected    string // game namespace, "" for none
	Placeholder string
	OnChanged   func(game string)
	// OnOpen is called as the list opens, so the window can make room for
	// it; NeededHeight says how tall the window must be.
	OnOpen func()

	games   []adapter.Title
	focused bool
	popup   *widget.PopUp
	list    *pickerList
	need    float32
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

// NeededHeight is how tall the window must be to show the open list in
// full, or 0 when the list is closed.
func (p *GamePicker) NeededHeight() float32 {
	if p.isOpen() {
		return p.need
	}
	return 0
}

// Tapped focuses the picker and opens the list.
func (p *GamePicker) Tapped(*fyne.PointEvent) {
	if p.Disabled() {
		return
	}
	if c := p.canvas(); c != nil {
		c.Focus(p)
	}
	p.open()
}

func (p *GamePicker) canvas() fyne.Canvas {
	return fyne.CurrentApp().Driver().CanvasForObject(p)
}

func (p *GamePicker) isOpen() bool { return p.popup != nil && p.popup.Visible() }

func (p *GamePicker) index(game string) int {
	for i, t := range p.games {
		if t.ID == game {
			return i
		}
	}
	return -1
}

func (p *GamePicker) open() {
	c := p.canvas()
	if c == nil || p.isOpen() {
		return
	}
	p.list = newPickerList(p.games, p.index(p.Selected), p.choose, p.close)
	p.popup = widget.NewPopUp(p.list, c)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(p)
	below := pos.Y + p.Size().Height
	p.need = below + p.popup.MinSize().Height + theme.Padding()
	// Make room first: a pop-up can't extend past the window's edge.
	if p.OnOpen != nil {
		p.OnOpen()
	}
	p.popup.ShowAtPosition(fyne.NewPos(pos.X, below))
	p.popup.Resize(fyne.NewSize(p.Size().Width, p.list.MinSize().Height))
	c.Focus(p.list)
}

// close shuts the list and puts focus back on the picker.
func (p *GamePicker) close() {
	if p.popup != nil {
		p.popup.Hide()
		p.popup, p.list = nil, nil
	}
	if c := p.canvas(); c != nil {
		c.Focus(p)
	}
}

func (p *GamePicker) choose(game string) {
	p.close()
	p.change(game)
}

func (p *GamePicker) change(game string) {
	if game == p.Selected {
		return
	}
	p.Selected = game
	p.Refresh()
	if p.OnChanged != nil {
		p.OnChanged(game)
	}
}

// step moves the selection by delta, stopping at either end.
func (p *GamePicker) step(delta int) {
	i := p.index(p.Selected) + delta
	if p.Selected == "" && delta < 0 {
		i = 0
	}
	if i < 0 || i >= len(p.games) {
		return
	}
	p.change(p.games[i].ID)
}

// FocusGained shows the focus ring.
func (p *GamePicker) FocusGained() {
	p.focused = true
	p.Refresh()
}

// FocusLost hides the focus ring.
func (p *GamePicker) FocusLost() {
	p.focused = false
	p.Refresh()
}

// TypedRune opens the list on Space, or jumps to the next game whose name
// starts with the letter typed.
func (p *GamePicker) TypedRune(r rune) {
	if p.Disabled() {
		return
	}
	if r == ' ' {
		p.open()
		return
	}
	if i := nextStartingWith(p.games, p.index(p.Selected), r); i >= 0 {
		p.change(p.games[i].ID)
	}
}

// TypedKey handles the arrow keys, Home, End and Enter.
func (p *GamePicker) TypedKey(e *fyne.KeyEvent) {
	if p.Disabled() {
		return
	}
	switch e.Name {
	case fyne.KeyDown:
		p.step(1)
	case fyne.KeyUp:
		p.step(-1)
	case fyne.KeyHome:
		p.change(p.games[0].ID)
	case fyne.KeyEnd:
		p.change(p.games[len(p.games)-1].ID)
	case fyne.KeyReturn, fyne.KeyEnter:
		p.open()
	}
}

// nextStartingWith returns the index of the next game after from whose name
// starts with r, wrapping round, or -1.
func nextStartingWith(games []adapter.Title, from int, r rune) int {
	want := unicode.ToLower(r)
	for n := 1; n <= len(games); n++ {
		i := (from + n + len(games)) % len(games)
		if first, _ := utf8.DecodeRuneInString(games[i].Name); unicode.ToLower(first) == want {
			return i
		}
	}
	return -1
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
	r.bg.StrokeWidth = 0
	if r.p.focused {
		r.bg.StrokeColor = theme.Color(theme.ColorNamePrimary)
		r.bg.StrokeWidth = 2
	}
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

// pickerList is the open list. It takes keyboard focus while open: Up and
// Down move the highlight, Home and End jump to either end, Enter or Space
// picks the highlighted game, and Escape closes without changing anything.
type pickerList struct {
	widget.BaseWidget
	games     []adapter.Title
	highlight int
	rows      []*pickerRow
	box       *fyne.Container
	choose    func(game string)
	cancel    func()
}

func newPickerList(games []adapter.Title, selected int, choose func(string), cancel func()) *pickerList {
	l := &pickerList{games: games, choose: choose, cancel: cancel, box: container.NewVBox()}
	for i, t := range games {
		row := newPickerRow(t, i == selected, l, i)
		l.rows = append(l.rows, row)
		l.box.Add(row)
	}
	l.ExtendBaseWidget(l)
	l.setHighlight(max(selected, 0))
	return l
}

func (l *pickerList) setHighlight(i int) {
	i = min(max(i, 0), len(l.rows)-1)
	l.highlight = i
	for j, row := range l.rows {
		if row.highlighted != (j == i) {
			row.highlighted = j == i
			row.Refresh()
		}
	}
}

func (l *pickerList) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(l.box) }

func (l *pickerList) FocusGained() {}
func (l *pickerList) FocusLost()   {}

func (l *pickerList) TypedRune(r rune) {
	if r == ' ' {
		l.choose(l.games[l.highlight].ID)
		return
	}
	if i := nextStartingWith(l.games, l.highlight, r); i >= 0 {
		l.setHighlight(i)
	}
}

func (l *pickerList) TypedKey(e *fyne.KeyEvent) {
	switch e.Name {
	case fyne.KeyDown:
		l.setHighlight(l.highlight + 1)
	case fyne.KeyUp:
		l.setHighlight(l.highlight - 1)
	case fyne.KeyHome:
		l.setHighlight(0)
	case fyne.KeyEnd:
		l.setHighlight(len(l.rows) - 1)
	case fyne.KeyReturn, fyne.KeyEnter:
		l.choose(l.games[l.highlight].ID)
	case fyne.KeyEscape:
		l.cancel()
	}
}

// pickerRow is one game in the open list.
type pickerRow struct {
	widget.BaseWidget
	title       adapter.Title
	selected    bool
	highlighted bool
	list        *pickerList
	index       int
}

func newPickerRow(t adapter.Title, selected bool, list *pickerList, index int) *pickerRow {
	r := &pickerRow{title: t, selected: selected, list: list, index: index}
	r.ExtendBaseWidget(r)
	return r
}

func (r *pickerRow) Tapped(*fyne.PointEvent)        { r.list.choose(r.title.ID) }
func (r *pickerRow) Cursor() desktop.Cursor         { return desktop.PointerCursor }
func (r *pickerRow) MouseIn(*desktop.MouseEvent)    { r.list.setHighlight(r.index) }
func (r *pickerRow) MouseMoved(*desktop.MouseEvent) {}
func (r *pickerRow) MouseOut()                      {}

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
	if rr.r.highlighted {
		rr.bg.FillColor = theme.Color(theme.ColorNameHover)
	}
	rr.bg.Refresh()
}

func (rr *rowRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{rr.bg, rr.content} }
func (rr *rowRenderer) Destroy()                     {}
