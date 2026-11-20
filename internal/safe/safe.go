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

//go:generate go run gen_tables.go

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
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

// Name cleans a short self-chosen label. Beyond Line, it removes what lets
// one name pass for another: invisible format characters, variation
// selectors, filler characters, and check marks (the client's verified mark
// is never part of a name). Runs of spaces collapse to one. A blank name
// becomes "?", so that a peer cannot be invisible.
func Name(s string) string {
	var b strings.Builder
	space := false
	for _, r := range clean(s, false, MaxName) {
		switch {
		case invisible(r), checkLike(r):
			continue
		case unicode.IsSpace(r):
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "?"
	}
	return b.String()
}

// Skeleton is what two names are compared by, after UTS #39: Name, then
// NFKC and case folding, then each character replaced by its confusables
// prototype (Cyrillic "а" becomes "a", "1" becomes "l"), and finally only
// letters and digits kept, so that marks, blanks, symbols and punctuation
// around a name do not make it different. Two names with one skeleton may
// look alike; two with different skeletons are not guaranteed not to.
func Skeleton(s string) string {
	return letters(proto(fold(proto(norm.NFD.String(fold(Name(s)))))))
}

// Key holds the two skeletons a name is compared by.
type Key [2]string

// KeyOf computes a name's Key: Skeleton, and a variant that maps
// prototypes before folding, so that "aIice" (capital I) matches "alice".
func KeyOf(s string) Key {
	strict := letters(proto(fold(proto(norm.NFD.String(norm.NFKC.String(Name(s)))))))
	return Key{Skeleton(s), strict}
}

// Like reports whether names with keys k and o may pass for each other.
func (k Key) Like(o Key) bool { return k[0] == o[0] || k[1] == o[1] }

// Confusable reports whether a and b may pass for each other.
func Confusable(a, b string) bool { return KeyOf(a).Like(KeyOf(b)) }

// Restricted reports whether a name uses a letter or digit outside the
// UTS #39 Allowed identifier set: small capitals, mathematical alphabets,
// historic scripts and the like, which confusables data does not fully
// cover. Spaces, punctuation and symbols do not count.
func Restricted(s string) bool {
	for _, r := range norm.NFC.String(Name(s)) {
		if (unicode.IsLetter(r) || unicode.IsDigit(r)) && !unicode.Is(allowed, r) {
			return true
		}
	}
	return false
}

func fold(s string) string { return norm.NFD.String(cases.Fold().String(norm.NFKC.String(s))) }

// proto replaces each character by its confusables prototype.
func proto(s string) string {
	var b strings.Builder
	for _, r := range s {
		if p, ok := confusables[r]; ok {
			b.WriteString(p)
		} else {
			b.WriteRune(r)
		}
	}
	return norm.NFD.String(b.String())
}

func letters(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// invisible reports characters that render as nothing: every
// Default_Ignorable_Code_Point (tables.go, generated from Unicode data,
// unassigned ones included), every format character, and the braille blank.
func invisible(r rune) bool {
	return unicode.Is(defaultIgnorable, r) || unicode.Is(unicode.Cf, r) || r == 0x2800
}

func checkLike(r rune) bool {
	switch r {
	case 0x2713, 0x2714, 0x2611, 0x2705, 0x221a, 0x1f5f8, 0x1f5f9, 0x237b, 0x10102:
		return true
	}
	return false
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
