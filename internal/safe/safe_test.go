package safe

import (
	"strings"
	"testing"
	"unicode"
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

// A name must not be able to look like another member's, or carry the
// verified mark. Byte escapes: U+200B ZWSP, U+2060 WJ, U+00AD soft hyphen,
// U+FE0F VS16, U+3164 Hangul filler, U+2713/2714/2611/2705 check marks.
func TestNameStripsInvisiblesAndChecks(t *testing.T) {
	cases := map[string]string{
		"alice\xe2\x80\x8b \xe2\x9c\x93": "alice",
		"al\xe2\x81\xa0ice":              "alice",
		"al\xc2\xadice":                  "alice",
		"alice\xef\xb8\x8f":              "alice",
		"\xe3\x85\xa4alice":              "alice",
		"\xe2\x9c\x94 bob \xe2\x98\x91":  "bob",
		"\xe2\x9c\x85carol":              "carol",
		"a   b":                          "a b",
		"\xe2\x80\x8b":                   "?",
	}
	for in, want := range cases {
		if got := Name(in); got != want {
			t.Errorf("Name(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSkeletonFoldsLookalikes(t *testing.T) {
	same := [][2]string{
		{"alice", "ALICE"},
		{"alice", "\xef\xbd\x81lice"}, // fullwidth a
		{"me", "Me\xe2\x80\x8b"},
		{"alice", "alice \xe2\x9c\x93"},
	}
	for _, p := range same {
		if Skeleton(p[0]) != Skeleton(p[1]) {
			t.Errorf("Skeleton(%q) != Skeleton(%q)", p[0], p[1])
		}
	}
	if Skeleton("alice") == Skeleton("alicia") {
		t.Error("different names share a skeleton")
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

func r(cp ...rune) string { return string(cp) }

// Every Default_Ignorable_Code_Point, assigned or not, is removed from
// names. Checked against Go's own Unicode tables, independently of the
// generated one: Other_Default_Ignorable_Code_Point and the variation
// selectors (with Cf, which TestNameStripsInvisiblesAndChecks covers).
func TestNameStripsEveryDefaultIgnorable(t *testing.T) {
	for _, tab := range []*unicode.RangeTable{unicode.Other_Default_Ignorable_Code_Point, unicode.Variation_Selector} {
		for _, rg := range tab.R16 {
			for c := rune(rg.Lo); c <= rune(rg.Hi); c += rune(rg.Stride) {
				if got := Name("a" + r(c) + "b"); got != "ab" {
					t.Errorf("U+%04X kept: %q", c, got)
				}
			}
		}
		for _, rg := range tab.R32 {
			for c := rune(rg.Lo); c <= rune(rg.Hi); c += rune(rg.Stride) {
				if got := Name("a" + r(c) + "b"); got != "ab" {
					t.Errorf("U+%04X kept: %q", c, got)
				}
			}
		}
	}
	// The review's examples.
	for _, c := range []rune{0x180b, 0x17b5, 0x17b4, 0x2065, 0xe0000, 0xe0fff, 0x1bca0, 0xfff0, 0xffa0, 0x115f, 0x1160, 0x3164, 0xfeff, 0x2064, 0x061c, 0x034f, 0xe0100} {
		if got := Name("alice" + r(c)); got != "alice" {
			t.Errorf("U+%04X kept: %q", c, got)
		}
	}
}

// Skeleton follows UTS #39: names that can look alike share one, including
// across scripts and with marks, blanks and symbols around the letters.
func TestSkeletonCatchesConfusables(t *testing.T) {
	alice := Skeleton("alice")
	for _, s := range []string{
		r(0x430) + "lice",      // Cyrillic a
		"al" + r(0x131) + "ce", // dotless i
		r(0x1d41a) + "lice",    // mathematical bold a
		"alice" + r(0x301),     // combining acute
		"alice" + r(0x1d159),   // musical null notehead
		"alice" + r(0x2e3a),    // two-em dash
		"alice" + r(0x16ed),    // runic cross punctuation
		r(0x221a) + "alice",    // square root
		"alice" + r(0xfffc),    // object replacement
		"alice" + r(0xa0),      // no-break space
		"ALICE",
		"a" + r(0x217c) + "ice", // small roman numeral fifty
		"a1ice",
	} {
		if Skeleton(s) != alice {
			t.Errorf("%+q: skeleton %+q, want %+q", s, Skeleton(s), alice)
		}
	}
	for _, s := range []string{"alicia", "bob", "malice", "ali"} {
		if Skeleton(s) == alice {
			t.Errorf("%q shares alice's skeleton", s)
		}
	}
}

func TestConfusableCatchesCapitalI(t *testing.T) {
	if !Confusable("aIice", "alice") || !Confusable("alice", "ALICE") || Confusable("alice", "bob") {
		t.Fatal("Confusable")
	}
}

// Letters outside UTS #39's Allowed identifier set (small capitals,
// mathematical letters, obsolete scripts) mark a name as restricted; the
// client then always shows its fingerprint tag. Names in living scripts
// are not restricted.
func TestRestricted(t *testing.T) {
	for _, s := range []string{r(0x1d00) + "lice", r(0x1d41a) + "lice", "al" + r(0x16a0) + "ce", r(0xa61c)} {
		if !Restricted(s) {
			t.Errorf("%+q not restricted", s)
		}
	}
	for _, s := range []string{"alice", "Zo" + r(0xeb), "bob smith", "x_1-2.3", r(0x410, 0x43b, 0x438, 0x441, 0x430), r(0x645, 0x62d, 0x645, 0x62f), r(0x674e), "alice " + r(0x2713)} {
		if Restricted(s) {
			t.Errorf("%+q restricted", s)
		}
	}
}
