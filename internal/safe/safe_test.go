package safe

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// hostile is a catalogue of what a peer might put in a name or a message to
// take over the terminal of whoever reads it. Byte escapes only, so that the
// source itself carries no invisible characters.
var hostile = []string{
	"\x1b[2J\x1b[H",    // clear screen, cursor home
	"\x1b]0;owned\x07", // set window title (OSC, BEL)
	"\x1b]8;;https://evil/\x1b\\x\x1b]8;;\x1b\\", // hyperlink (OSC 8, ST)
	"\x1b]52;c;cm0gLXJmIH4=\x07",                 // write the clipboard (OSC 52)
	"\x1bP+q544e\x1b\\",                          // DCS query
	"\x9b31m",                                    // 8-bit CSI (invalid UTF-8)
	"\x9d0;owned\x9c",                            // 8-bit OSC and ST
	"\x1b[6n",                                    // cursor position report (input injection)
	"a\rb",                                       // carriage return overwrite
	"a\bb",                                       // backspace overwrite
	"\x07\x00\x7f",                               // bell, NUL, DEL
	"\xc2\x9b31m",                                // C1 CSI as valid UTF-8
	"\xff\xfe\x1b",                               // invalid UTF-8 around ESC
	"evil\xe2\x80\xaegnp.exe",                    // U+202E right-to-left override
	"\xe2\x81\xa6x\xe2\x81\xa9",                  // U+2066, U+2069 isolates
}

func controlFree(t *testing.T, s string, keepNewline bool) {
	t.Helper()
	if !utf8.ValidString(s) {
		t.Fatalf("output is not valid UTF-8: %q", s)
	}
	for _, r := range s {
		switch {
		case r == '\n' && keepNewline:
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			t.Fatalf("control rune %U survived in %q", r, s)
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
			t.Fatalf("bidi override %U survived in %q", r, s)
		}
	}
}

func TestLineRemovesEveryControl(t *testing.T) {
	for _, in := range hostile {
		controlFree(t, Line(in), false)
		controlFree(t, Line(in+"\nsecond line"), false)
	}
}

func TestTextKeepsNewlinesOnly(t *testing.T) {
	for _, in := range hostile {
		controlFree(t, Text(in+"\nnext"), true)
	}
	// CRLF, and U+2028 LINE SEPARATOR, become \n.
	if got := Text("one\r\ntwo\xe2\x80\xa8three"); got != "one\ntwo\nthree" {
		t.Fatalf("Text newlines = %q", got)
	}
}

func TestControlsAreShownNotDropped(t *testing.T) {
	// A reader should see that something odd was sent, not a silently
	// different message.
	if got := Line("a\x1b[31mb"); got != "a^[[31mb" {
		t.Fatalf("Line(ESC) = %q", got)
	}
	if got := Line("a\rb"); got != "a^Mb" {
		t.Fatalf("Line(CR) = %q", got)
	}
	if got := Line("a\xc2\x9bb"); got != "a"+string(utf8.RuneError)+"b" {
		t.Fatalf("Line(C1) = %q", got)
	}
}

func TestOrdinaryTextUntouched(t *testing.T) {
	for _, s := range []string{
		"hello", "naïve café", "مرحبا بالعالم", "日本語",
		"a\xe2\x80\x8fb", // U+200F RIGHT-TO-LEFT MARK is a mark, not an override
		"ok: 100% #general",
	} {
		if got := Line(s); got != s {
			t.Fatalf("Line(%q) = %q", s, got)
		}
	}
	if got := Line("a\tb"); got != "a    b" {
		t.Fatalf("tab = %q", got)
	}
}

func TestLimits(t *testing.T) {
	long := strings.Repeat("x", MaxText+100)
	if got := Text(long); utf8.RuneCountInString(got) != MaxText+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("Text did not cap: %d runes", utf8.RuneCountInString(got))
	}
	if got := Name(strings.Repeat("n", 500)); utf8.RuneCountInString(got) != MaxName+1 {
		t.Fatalf("Name did not cap: %d runes", utf8.RuneCountInString(got))
	}
	if got := Name("   "); got != "?" {
		t.Fatalf("blank name = %q", got)
	}
}

func FuzzLine(f *testing.F) {
	for _, s := range hostile {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		controlFree(t, Line(s), false)
		controlFree(t, Text(s), true)
	})
}
