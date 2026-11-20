package ui

import (
	"charm.land/lipgloss/v2"
)

// theme is a handful of styles, as functions so that plain mode can be the
// identity. Only foreground colours and weight: no backgrounds, so the
// terminal's own light or dark scheme shows through.
type theme struct {
	dim, accent, accentBold, bold, good, bad, italic func(string) string
}

func newTheme(dark, plain bool) theme {
	if plain {
		id := func(s string) string { return s }
		return theme{id, id, id, id, id, id, id}
	}
	pick := lipgloss.LightDark(dark)
	fg := func(light, dark string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(pick(lipgloss.Color(light), lipgloss.Color(dark)))
	}
	r := func(s lipgloss.Style) func(string) string {
		return func(t string) string { return s.Render(t) }
	}
	dim := fg("#8c8fa1", "#6c7086")
	accent := fg("#1e66f5", "#89b4fa")
	return theme{
		dim:        r(dim),
		accent:     r(accent),
		accentBold: r(accent.Bold(true)),
		bold:       r(lipgloss.NewStyle().Bold(true)),
		good:       r(fg("#40a02b", "#a6e3a1")),
		bad:        r(fg("#d20f39", "#f38ba8")),
		italic:     r(dim.Italic(true)),
	}
}
