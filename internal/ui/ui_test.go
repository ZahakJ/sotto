package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZahakJ/sotto/internal/fakebot"
	"github.com/ZahakJ/sotto/internal/proto"
	"github.com/ZahakJ/sotto/internal/trust"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakebot.Env) == "1" {
		os.Exit(fakebot.Main(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// recorder stands in for the bot process: it records commands.
type recorder struct {
	cmds []proto.Command
	next uint64
}

func (r *recorder) Send(c proto.Command) (uint64, error) {
	r.next++
	c.ID = r.next
	r.cmds = append(r.cmds, c)
	return r.next, nil
}

func (r *recorder) last(cmd string) (proto.Command, bool) {
	for i := len(r.cmds) - 1; i >= 0; i-- {
		if r.cmds[i].Cmd == cmd {
			return r.cmds[i], true
		}
	}
	return proto.Command{}, false
}

func ready(hostile bool) proto.Event {
	den, gen := "Den", "general"
	if hostile {
		// Already cleaned, as proto.Decode hands it over; the model must
		// never undo that, and must not choke on the caret forms.
		den, gen = "Den^[]0;owned^G", "gen^[[2Jeral"
	}
	return proto.Event{
		Type: "ready", Fingerprint: fakebot.SelfFP, PeerID: "p", Name: "me",
		Guilds: []proto.Guild{
			{ID: "g-den", Name: den, Channels: []proto.Channel{
				{ID: "c-gen", Name: gen}, {ID: "c-rand", Name: "random"}, {ID: "c-voice", Name: "lounge", Type: "voice"},
			}},
			{ID: "g-dm", Name: "alice", DM: true, Channels: []proto.Channel{{ID: "c-dm", Name: "dm"}}},
		},
	}
}

func newModel(t *testing.T, w, h int, plain bool) (*Model, *recorder) {
	t.Helper()
	store, err := trust.Load(filepath.Join(t.TempDir(), "verified.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	m := New(r, store, ready(false), Options{Plain: plain})
	m.update(tea.WindowSizeMsg{Width: w, Height: h})
	return m, r
}

func (m *Model) update(msg tea.Msg) {
	m.Update(msg)
}

func (m *Model) typeText(s string) {
	for _, r := range s {
		m.update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (m *Model) key(code rune, mod tea.KeyMod) {
	m.update(tea.KeyPressMsg{Code: code, Mod: mod})
}

func (m *Model) enter() { m.key(tea.KeyEnter, 0) }

func (m *Model) screen() string { return m.View().Content }

func members(fps ...string) proto.Event {
	e := proto.Event{Type: "members", GuildID: "g-den"}
	for i := 0; i < len(fps); i += 2 {
		e.Members = append(e.Members, proto.Member{Fingerprint: fps[i], Name: fps[i+1]})
	}
	return e
}

func msgEvent(ch, id, fp, name, text string) proto.Event {
	return proto.Event{Type: "message", GuildID: "g-den", ChannelID: ch, MessageID: id,
		Sender: fp, SenderName: name, Content: text, Sent: "2026-11-19T10:04:00Z"}
}

// ---- Escape injection ----

// js quotes s as a JSON string, the way concord's encoder writes it: raw
// bytes 0x80-0xff that are not UTF-8 become U+FFFD, controls become \u00XX.
func js(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// assertClean fails if anything but the client's own colouring could reach
// the terminal: no ESC outside an SGR colour sequence, no C1, no other C0.
func assertClean(t *testing.T, screen string) {
	t.Helper()
	rest := sgr.ReplaceAllString(screen, "")
	for i, r := range rest {
		if r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || (r == utf8.RuneError && !strings.HasPrefix(rest[i:], "\xef\xbf\xbd")) {
			t.Fatalf("control %U reached the screen at byte %d: %q", r, i, rest[max(0, i-20):min(len(rest), i+20)])
		}
	}
}

func TestEscapeInjectionThroughEveryRemoteField(t *testing.T) {
	for _, plain := range []bool{true, false} {
		store, _ := trust.Load(filepath.Join(t.TempDir(), "v.json"))
		r := &recorder{}
		m := New(r, store, ready(true), Options{Plain: plain})
		m.update(tea.WindowSizeMsg{Width: 80, Height: 24})
		// The raw strings go through proto.Decode, as they would from the
		// child, and then into the model.
		raw := []string{
			fmt.Sprintf(`{"type":"members","guildId":"g-den","members":[{"fingerprint":%s,"name":%s},{"fingerprint":"X\u001b[2J","name":"ok"}]}`, js(fakebot.MalloryFP), js(fakebot.EvilName)),
			fmt.Sprintf(`{"type":"message","guildId":"g-den","channelId":"c-gen","messageId":"1","sender":%s,"senderName":%s,"content":%s,"sent":"2026-11-19T10:00:00Z"}`, js(fakebot.MalloryFP), js(fakebot.EvilName), js(fakebot.EvilContent)),
			fmt.Sprintf(`{"type":"message","guildId":"g-den","channelId":"c-rand","messageId":"2","sender":%s,"senderName":%s,"content":"x","sent":"\u001b[2J"}`, js(fakebot.MalloryFP), js(fakebot.EvilName)),
			`{"type":"edit","guildId":"g-den","channelId":"c-gen","messageId":"1","content":"edited\u001b]0;t\u0007"}`,
			fmt.Sprintf(`{"type":"member","action":"join","guildId":"g-den","guild":"Den\u001b[5m","fingerprint":"F","name":%s}`, js(fakebot.EvilName)),
			`{"type":"error","error":"no channel \"\u001b]52;c;eA==\u0007\""}`,
			`{"type":"guild","action":"join","guildId":"g-x","guild":"\u009b2J"}`,
		}
		for _, line := range raw {
			e, err := proto.Decode([]byte(line))
			if err != nil {
				t.Fatalf("decode %s: %v", line, err)
			}
			m.update(EventMsg{e})
			assertClean(t, m.screen())
		}
		// Local input is also cleaned: a paste can carry escapes too.
		m.update(tea.PasteMsg{Content: "paste\x1b[2J\x1b]0;x\x07"})
		assertClean(t, m.screen())
		m.enter()
		assertClean(t, m.screen())
		for _, overlay := range []rune{'k'} {
			m.key(overlay, tea.ModCtrl)
			assertClean(t, m.screen())
			m.key(tea.KeyEscape, 0)
		}
		m.key(tea.KeyTab, 0) // members
		assertClean(t, m.screen())
		if plain && strings.ContainsRune(m.screen(), 0x1b) {
			t.Fatal("plain mode emitted an escape")
		}
	}
	// The attempt is visible, not silently dropped.
	m, _ := newModel(t, 120, 30, true)
	m.update(EventMsg{proto.Event{Type: "message", ChannelID: "c-gen", MessageID: "1", Sender: fakebot.MalloryFP,
		SenderName: "mallory", Content: "x^[[2Jy", Sent: "2026-11-19T10:00:00Z"}})
	if !strings.Contains(m.screen(), "x^[[2Jy") {
		t.Fatalf("caret form not displayed:\n%s", m.screen())
	}
}

// ---- Layout ----

func TestLayoutFitsEverySize(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {200, 50}, {60, 16}, {40, 10}, {20, 5}} {
		m, _ := newModel(t, sz[0], sz[1], false)
		m.update(EventMsg{members(fakebot.AliceFP, "alice", fakebot.SelfFP, "me")})
		m.update(EventMsg{msgEvent("c-gen", "1", fakebot.AliceFP, "alice", strings.Repeat("a long line that wraps ", 30))})
		m.update(EventMsg{msgEvent("c-gen", "2", fakebot.AliceFP, "alice", "日本語のテキスト "+strings.Repeat("広い", 50))})
		for _, view := range []func(){func() {}, func() { m.key('k', tea.ModCtrl) }, func() { m.key(tea.KeyEscape, 0); m.key(tea.KeyTab, 0) }} {
			view()
			lines := strings.Split(m.screen(), "\n")
			if len(lines) != sz[1] {
				t.Fatalf("%dx%d: %d lines", sz[0], sz[1], len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w > sz[0] {
					t.Fatalf("%dx%d: line %d is %d wide: %q", sz[0], sz[1], i, w, l)
				}
			}
		}
	}
}

// ---- Behaviour ----

func TestUnreadCountsAndSwitching(t *testing.T) {
	m, _ := newModel(t, 80, 24, true)
	if m.active != "c-gen" {
		t.Fatalf("active = %q", m.active)
	}
	m.update(EventMsg{msgEvent("c-rand", "1", fakebot.AliceFP, "alice", "psst")})
	m.update(EventMsg{msgEvent("c-rand", "2", fakebot.AliceFP, "alice", "psst again")})
	m.update(EventMsg{msgEvent("c-gen", "3", fakebot.AliceFP, "alice", "here")})
	if !regexp.MustCompile(`# random +2`).MatchString(m.screen()) {
		t.Fatalf("no unread count for #random:\n%s", m.screen())
	}
	if regexp.MustCompile(`# general +\d`).MatchString(m.screen()) {
		t.Fatalf("active channel shows unread:\n%s", m.screen())
	}
	m.key('k', tea.ModCtrl)
	m.typeText("rand")
	m.enter()
	if m.active != "c-rand" {
		t.Fatalf("ctrl-k did not switch: %q", m.active)
	}
	if m.unread["c-rand"] != 0 || !strings.Contains(m.screen(), "psst again") {
		t.Fatalf("switching did not clear unread or show messages:\n%s", m.screen())
	}
	if strings.Contains(m.screen(), "lounge") {
		t.Fatal("voice channel listed")
	}
}

func TestSendEchoesThenConfirmsOrFails(t *testing.T) {
	m, r := newModel(t, 80, 24, true)
	m.typeText("hello there")
	m.enter()
	c, ok := r.last("send")
	if !ok || c.Channel != "c-gen" || c.Text != "hello there" {
		t.Fatalf("send = %+v", c)
	}
	if !strings.Contains(m.screen(), "hello there") || !strings.Contains(m.screen(), "sending") {
		t.Fatalf("no pending echo:\n%s", m.screen())
	}
	m.update(EventMsg{proto.Event{Type: "sent", ID: []byte(fmt.Sprint(c.ID)), ChannelID: "c-gen", MessageID: "m1"}})
	if strings.Contains(m.screen(), "sending") {
		t.Fatalf("still pending after sent:\n%s", m.screen())
	}
	m.typeText("second")
	m.enter()
	c2, _ := r.last("send")
	m.update(EventMsg{proto.Event{Type: "error", ID: []byte(fmt.Sprint(c2.ID)), Error: "no peers"}})
	if !strings.Contains(m.screen(), "not sent") {
		t.Fatalf("failure not shown:\n%s", m.screen())
	}
	// "//" sends a message that starts with a slash.
	m.typeText("//shrug")
	m.enter()
	if c3, _ := r.last("send"); c3.Text != "/shrug" {
		t.Fatalf("escaped slash sent %q", c3.Text)
	}
}

func TestJoinCommand(t *testing.T) {
	m, r := newModel(t, 80, 24, true)
	m.typeText("/join CI1ARDg")
	m.enter()
	if c, ok := r.last("join"); !ok || c.Invite != "CI1ARDg" {
		t.Fatalf("join = %+v", c)
	}
	m.update(EventMsg{proto.Event{Type: "joined", ID: []byte(fmt.Sprint(r.next)),
		Joined: &proto.Guild{ID: "g-ws", Name: "Workshop", Channels: []proto.Channel{{ID: "c-ws", Name: "general"}}}}})
	if _, ok := r.last("list"); !ok {
		t.Fatal("no list after joining")
	}
	if !strings.Contains(m.screen(), "Workshop") {
		t.Fatalf("joined guild not shown:\n%s", m.screen())
	}
	m.typeText("/invite")
	m.enter()
	if !strings.Contains(m.screen(), "/join") {
		t.Fatalf("/invite gave no guidance:\n%s", m.screen())
	}
}

func TestVerifyNeedsTheWholeFingerprint(t *testing.T) {
	m, _ := newModel(t, 120, 30, true)
	m.update(EventMsg{members(fakebot.SelfFP, "me", fakebot.AliceFP, "alice")})
	m.typeText("/verify alice")
	m.enter()
	if m.trust.Verified(fakebot.AliceFP) {
		t.Fatal("a name alone verified")
	}
	if !strings.Contains(m.screen(), fakebot.AliceFP) {
		t.Fatalf("full fingerprint not shown for comparison:\n%s", m.screen())
	}
	m.typeText("/verify " + strings.ToLower(fakebot.AliceFP))
	m.enter()
	if !m.trust.Verified(fakebot.AliceFP) {
		t.Fatalf("full fingerprint did not verify:\n%s", m.screen())
	}
	m.key(tea.KeyTab, 0)
	if !regexp.MustCompile(`✓ alice`).MatchString(m.screen()) {
		t.Fatalf("members pane lacks verified mark:\n%s", m.screen())
	}
	m.typeText("/unverify alice")
	m.enter()
	if m.trust.Verified(fakebot.AliceFP) {
		t.Fatal("unverify failed")
	}
}

func TestSameNameIsDisambiguated(t *testing.T) {
	m, _ := newModel(t, 100, 24, true)
	m.update(EventMsg{members(fakebot.AliceFP, "alice", fakebot.MalloryFP, "alice")})
	m.update(EventMsg{msgEvent("c-gen", "1", fakebot.MalloryFP, "alice", "it's me, alice")})
	if !strings.Contains(m.screen(), "alice·MALL") {
		t.Fatalf("impostor not disambiguated:\n%s", m.screen())
	}
}

// Ported from the review's TestPoCSpoof: an impostor named "alice<ZWSP> ✓"
// must not look like verified alice, and "me<ZWSP>" must not look like the
// user. The mark lives in its own column, before the name.
func TestSpoofedNamesAreTaggedAndCannotFakeTheMark(t *testing.T) {
	line := func(screen, needle string) string {
		for _, l := range strings.Split(screen, "\n") {
			if strings.Contains(l, needle) {
				return l
			}
		}
		return ""
	}
	for _, plain := range []bool{true, false} {
		m, _ := newModel(t, 120, 30, plain)
		m.update(EventMsg{members(fakebot.SelfFP, "me", fakebot.AliceFP, "alice", fakebot.MalloryFP, "x")})
		if err := m.trust.Mark(fakebot.AliceFP, "alice"); err != nil {
			t.Fatal(err)
		}
		dec := func(id, fp, name, text string) {
			e, err := proto.Decode([]byte(`{"type":"message","guildId":"g-den","channelId":"c-gen","messageId":"` + id +
				`","sender":"` + fp + `","senderName":` + js(name) + `,"content":"` + text + `","sent":"2026-11-19T10:04:00Z"}`))
			if err != nil {
				t.Fatal(err)
			}
			m.update(EventMsg{e})
		}
		dec("1", fakebot.AliceFP, "alice", "REAL")
		dec("2", fakebot.MalloryFP, "alice\xe2\x80\x8b \xe2\x9c\x93", "FAKE1")
		dec("3", fakebot.MalloryFP, "me\xe2\x80\x8b", "FAKE2")
		s := sgr.ReplaceAllString(m.screen(), "")
		real, fake, self := line(s, "REAL"), line(s, "FAKE1"), line(s, "FAKE2")
		if !regexp.MustCompile(`\d\d:\d\d ✓ alice  REAL`).MatchString(real) {
			t.Errorf("plain=%v real alice: %q", plain, real)
		}
		if strings.Contains(fake, "✓") || !strings.Contains(fake, "alice·MALL") {
			t.Errorf("plain=%v impostor: %q", plain, fake)
		}
		if !strings.Contains(self, "me·MALL") {
			t.Errorf("plain=%v self impostor: %q", plain, self)
		}
	}
}

func TestDroppedReplayIsMarked(t *testing.T) {
	m, _ := newModel(t, 80, 24, true)
	m.update(EventMsg{proto.Event{Type: "replayed", Count: 500, Dropped: 12}})
	if s := m.screen(); !strings.Contains(s, "12 earlier messages dropped") {
		t.Fatalf("no dropped marker:\n%s", s)
	}
	m.update(tea.KeyPressMsg{Code: 'x', Text: "x"}) // a keypress clears notices, not this
	if s := m.screen(); !strings.Contains(s, "12 earlier messages dropped") {
		t.Fatalf("dropped marker did not last:\n%s", s)
	}
}

func TestRenamedMemberIsFollowed(t *testing.T) {
	m, _ := newModel(t, 120, 30, true)
	m.update(EventMsg{members(fakebot.AliceFP, "ALIC")})
	m.update(EventMsg{msgEvent("c-gen", "1", fakebot.AliceFP, "alice", "renamed")})
	m.typeText("/verify alice")
	m.enter()
	if !strings.Contains(m.screen(), fakebot.AliceFP) {
		t.Fatalf("new name not resolved:\n%s", m.screen())
	}
}

func TestQuit(t *testing.T) {
	m, _ := newModel(t, 80, 24, true)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl-c on an empty line did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl-c returned something other than quit")
	}
}

// Ported from the re-review's TestPoCSpoof2: every name that can pass for
// verified alice gets the fingerprint tag.
func TestLookalikeNamesAreTagged(t *testing.T) {
	r := func(cp ...rune) string { return string(cp) }
	cands := map[string]string{
		"zwsp": "alice" + r(0x200b), "check": "alice " + r(0x2713), "mongolFVS": "alice" + r(0x180b),
		"khmer17b5": "alice" + r(0x17b5), "khmer17b4": "al" + r(0x17b4) + "ice", "objrepl": "alice" + r(0xfffc),
		"nullnote": "alice" + r(0x1d159), "unassignedDI": "alice" + r(0x2065), "tagUnassigned": "alice" + r(0xe0000),
		"E0FFF": "alice" + r(0xe0fff), "cgj": "alice" + r(0x34f), "cyrillic": r(0x430) + "lice",
		"fullwidth": r(0xff41) + "lice", "combining": "alice" + r(0x301), "nbsp": "alice" + r(0xa0),
		"heavycheck": r(0x2714, 0xfe0f) + " alice", "ballot": r(0x1f5f8) + " alice", "smallcaps": r(0x1d00) + "lice",
		"mathbold": r(0x1d41a) + "lice", "dotless": "al" + r(0x131) + "ce", "FFF0": "alice" + r(0xfff0),
		"hangulHF": "alice" + r(0xffa0), "1BCA0": "alice" + r(0x1bca0), "ALM": "alice" + r(0x61c),
		"U+2064": "alice" + r(0x2064), "U+3164": "alice" + r(0x3164), "VS17": "alice" + r(0xe0100),
		"U+1160": "alice" + r(0x1160), "BOM": r(0xfeff) + "alice", "sqrt": r(0x221a) + "alice",
		"runic": "alice" + r(0x16ed), "twoem": "alice" + r(0x2e3a),
	}
	m, _ := newModel(t, 120, 60, true)
	m.update(EventMsg{members(fakebot.SelfFP, "me", fakebot.AliceFP, "alice", fakebot.MalloryFP, "x")})
	if err := m.trust.Mark(fakebot.AliceFP, "alice"); err != nil {
		t.Fatal(err)
	}
	i := 0
	for k, v := range cands {
		i++
		e, err := proto.Decode([]byte(`{"type":"message","guildId":"g-den","channelId":"c-gen","messageId":"` + strconv.Itoa(i) +
			`","sender":"` + fakebot.MalloryFP + `","senderName":` + js(v) + `,"content":"T-` + k + `-","sent":"2026-11-19T10:04:00Z"}`))
		if err != nil {
			t.Fatal(err)
		}
		m.update(EventMsg{e})
		var l string
		for _, x := range strings.Split(m.screen(), "\n") {
			if strings.Contains(x, "T-"+k+"-") {
				l = x
			}
		}
		if !strings.Contains(l, "·MALL") || strings.Contains(l, "✓") {
			t.Errorf("%s (%+q): %q", k, e.SenderName, strings.TrimSpace(l))
		}
	}
}
