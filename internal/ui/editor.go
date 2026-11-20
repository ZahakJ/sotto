package ui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// editor is a one-line input with the usual readline keys.
type editor struct {
	r   []rune
	pos int
}

func (e *editor) String() string { return string(e.r) }
func (e *editor) empty() bool    { return len(e.r) == 0 }

func (e *editor) set(s string) {
	e.r = []rune(s)
	e.pos = len(e.r)
}

func (e *editor) insert(s string) {
	ins := []rune(cleanInput(s))
	e.r = append(e.r[:e.pos], append(ins, e.r[e.pos:]...)...)
	e.pos += len(ins)
}

// key applies an editing key and reports whether it was one.
func (e *editor) key(k tea.KeyPressMsg) bool {
	switch k.String() {
	case "left", "ctrl+b":
		e.pos = max(0, e.pos-1)
	case "right", "ctrl+f":
		e.pos = min(len(e.r), e.pos+1)
	case "home", "ctrl+a":
		e.pos = 0
	case "end", "ctrl+e":
		e.pos = len(e.r)
	case "alt+left", "alt+b":
		e.pos = e.wordStart()
	case "alt+right", "alt+f":
		for e.pos < len(e.r) && unicode.IsSpace(e.r[e.pos]) {
			e.pos++
		}
		for e.pos < len(e.r) && !unicode.IsSpace(e.r[e.pos]) {
			e.pos++
		}
	case "backspace", "ctrl+h":
		if e.pos > 0 {
			e.r = append(e.r[:e.pos-1], e.r[e.pos:]...)
			e.pos--
		}
	case "delete", "ctrl+d":
		if e.pos < len(e.r) {
			e.r = append(e.r[:e.pos], e.r[e.pos+1:]...)
		}
	case "ctrl+w", "alt+backspace":
		start := e.wordStart()
		e.r = append(e.r[:start], e.r[e.pos:]...)
		e.pos = start
	case "ctrl+u":
		e.r = e.r[e.pos:]
		e.pos = 0
	default:
		if k.Text == "" || k.Mod&(tea.ModCtrl|tea.ModAlt) != 0 {
			return false
		}
		e.insert(k.Text)
	}
	return true
}

func (e *editor) wordStart() int {
	i := e.pos
	for i > 0 && unicode.IsSpace(e.r[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(e.r[i-1]) {
		i--
	}
	return i
}

// view returns the visible slice for a field width cells wide and the
// cursor's column within it, scrolling horizontally to keep it in view.
func (e *editor) view(width int) (string, int) {
	if width <= 1 {
		return "", 0
	}
	start := 0
	for ansi.StringWidth(string(e.r[start:e.pos])) > width-1 {
		start++
	}
	var b strings.Builder
	w := 0
	for _, r := range e.r[start:] {
		rw := ansi.StringWidth(string(r))
		if w+rw > width {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String(), ansi.StringWidth(string(e.r[start:e.pos]))
}

// cleanInput keeps typed or pasted text printable: line breaks and tabs
// become spaces, every other control and bidi override is dropped.
func cleanInput(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n', r == '\r', r == '\t':
			b.WriteByte(' ')
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, r == 0xfffd:
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
