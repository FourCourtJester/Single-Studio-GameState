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
		// Fyne's default is too faint to read on the dark background. Kept
		// well below normal text so disabled buttons still look disabled.
		if dark {
			return color.NRGBA{0x80, 0x80, 0x8a, 0xff}
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

// githubIcon is GitHub's mark (Octicons "mark-github"), recoloured to match
// the theme.
var githubIcon = theme.NewThemedResource(fyne.NewStaticResource("github.svg", []byte(
	`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="#000"><path d="M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z"/></svg>`)))

// Sun and moon icons for the theme button, recoloured to match the theme.
// Fyne recolours fills, not strokes, so both are drawn as filled shapes.
var (
	sunIcon = theme.NewThemedResource(fyne.NewStaticResource("sun.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#000"><circle cx="12" cy="12" r="4.5"/><polygon points="19.60,13.00 22.60,13.00 22.60,11.00 19.60,11.00"/><polygon points="16.67,18.08 18.79,20.20 20.20,18.79 18.08,16.67"/><polygon points="11.00,19.60 11.00,22.60 13.00,22.60 13.00,19.60"/><polygon points="5.92,16.67 3.80,18.79 5.21,20.20 7.33,18.08"/><polygon points="4.40,11.00 1.40,11.00 1.40,13.00 4.40,13.00"/><polygon points="7.33,5.92 5.21,3.80 3.80,5.21 5.92,7.33"/><polygon points="13.00,4.40 13.00,1.40 11.00,1.40 11.00,4.40"/><polygon points="18.08,7.33 20.20,5.21 18.79,3.80 16.67,5.92"/></svg>`)))
	moonIcon = theme.NewThemedResource(fyne.NewStaticResource("moon.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#000">`+
			`<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/></svg>`)))
)
