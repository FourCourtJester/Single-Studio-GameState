package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// panelTheme pins Fyne's default theme to one variant, so the panel is dark
// by default whatever the OS is set to, and light only when asked.
type panelTheme struct {
	variant fyne.ThemeVariant
}

func newTheme(dark bool) fyne.Theme {
	if dark {
		return panelTheme{theme.VariantDark}
	}
	return panelTheme{theme.VariantLight}
}

func (t panelTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	dark := t.variant == theme.VariantDark
	switch name {
	case theme.ColorNamePrimary:
		if dark {
			return color.NRGBA{0x3b, 0x82, 0xf6, 0xff}
		}
		return color.NRGBA{0x25, 0x63, 0xeb, 0xff}
	case theme.ColorNameDisabled:
		// Low-importance labels (captions, status lines) use this colour;
		// Fyne's default is too faint to read on the dark background.
		if dark {
			return color.NRGBA{0x9a, 0x9a, 0xa3, 0xff}
		}
		return color.NRGBA{0x71, 0x71, 0x7a, 0xff}
	}
	return theme.DefaultTheme().Color(name, t.variant)
}

func (t panelTheme) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (t panelTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }
func (t panelTheme) Size(n fyne.ThemeSizeName) float32       { return theme.DefaultTheme().Size(n) }

// errorColors returns the error pane's background and border.
func errorColors(dark bool) (bg, border color.Color) {
	if dark {
		return color.NRGBA{0x2a, 0x12, 0x15, 0xff}, color.NRGBA{0x7f, 0x1d, 0x1d, 0xff}
	}
	return color.NRGBA{0xfe, 0xf2, 0xf2, 0xff}, color.NRGBA{0xfe, 0xca, 0xca, 0xff}
}

// Sun and moon icons for the theme button, recoloured to match the theme.
// Fyne recolours fills, not strokes, so both are drawn as filled shapes.
var (
	sunIcon = theme.NewThemedResource(fyne.NewStaticResource("sun.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#000"><circle cx="12" cy="12" r="4.5"/><polygon points="19.60,13.00 22.60,13.00 22.60,11.00 19.60,11.00"/><polygon points="16.67,18.08 18.79,20.20 20.20,18.79 18.08,16.67"/><polygon points="11.00,19.60 11.00,22.60 13.00,22.60 13.00,19.60"/><polygon points="5.92,16.67 3.80,18.79 5.21,20.20 7.33,18.08"/><polygon points="4.40,11.00 1.40,11.00 1.40,13.00 4.40,13.00"/><polygon points="7.33,5.92 5.21,3.80 3.80,5.21 5.92,7.33"/><polygon points="13.00,4.40 13.00,1.40 11.00,1.40 11.00,4.40"/><polygon points="18.08,7.33 20.20,5.21 18.79,3.80 16.67,5.92"/></svg>`)))
	moonIcon = theme.NewThemedResource(fyne.NewStaticResource("moon.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#000">`+
			`<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/></svg>`)))
)
