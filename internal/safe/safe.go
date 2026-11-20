// Package safe makes remote text harmless to print.
//
// Everything a peer controls (names, messages, guild and channel names, even
// error strings that echo them) passes through here before it reaches the
// model, so nothing a peer sends can move the cursor, retitle the window,
// write the clipboard, plant a hyperlink or answer a terminal query.
//
// Controls are made visible rather than dropped: ESC prints as ^[, CR as ^M,
// an 8-bit control as U+FFFD. A reader sees that something odd was sent.
package safe

import (
	"strings"
	"unicode/utf8"
)

const (
	// MaxText caps a message body as displayed, in runes.
	MaxText = 8000
	// MaxName caps a display, guild or channel name, in runes.
	MaxName = 64
)

const (
	lineSep = 0x2028 // LINE SEPARATOR
	paraSep = 0x2029 // PARAGRAPH SEPARATOR
)

// Line cleans text meant for a single line: every newline becomes a space.
func Line(s string) string { return clean(s, false, MaxText) }

// Text cleans a message body, keeping its line breaks (normalised to \n).
func Text(s string) string { return clean(s, true, MaxText) }

// Name cleans a short self-chosen label. A blank one becomes "?", so that a
// peer cannot be invisible.
func Name(s string) string {
	out := strings.TrimSpace(clean(s, false, MaxName))
	if out == "" {
		return "?"
	}
	return out
}

func clean(s string, keepNewlines bool, max int) string {
	var b strings.Builder
	b.Grow(len(s))
	n := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if n == max {
			b.WriteRune('…')
			break
		}
		n++
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError) // invalid byte: never pass it through raw
		case r == '\r' && i < len(s) && s[i] == '\n':
			n-- // CRLF counts once, as its LF
		case r == '\n', r == lineSep, r == paraSep:
			if keepNewlines {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
		case r == '\t':
			b.WriteString("    ")
		case r < 0x20:
			b.WriteByte('^')
			b.WriteByte(byte(r) + '@')
		case r == 0x7f:
			b.WriteString("^?")
		case r >= 0x80 && r <= 0x9f:
			b.WriteRune(utf8.RuneError) // C1 controls, including 8-bit CSI and OSC
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
			// Bidi embeddings, overrides and isolates reorder what follows
			// them, which lets a name or message display as something else.
			// The plain marks (LRM, RLM, ALM) stay: they do not override.
			n--
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
