// Package ui is the terminal interface: channels on the left, messages in
// the middle, one input line. Every remote string it holds has already been
// cleaned by package proto; local input is cleaned by cleanInput.
package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZahakJ/concord-cli/internal/proto"
	"github.com/ZahakJ/concord-cli/internal/safe"
	"github.com/ZahakJ/concord-cli/internal/trust"
)

// Sender is the bot connection, local or attached over a mesh.
type Sender interface {
	Send(proto.Command) (uint64, error)
}

// Options tune the interface.
type Options struct {
	Plain  bool // no colour or attributes at all
	Remote bool // attached to a host: quitting detaches, /shutdown stops it
}

// EventMsg carries one protocol event into the program.
type EventMsg struct{ proto.Event }

// ExitMsg says the connection ended.
type ExitMsg struct{ Err error }

const maxPerChannel = 2000

type overlay int

const (
	ovNone overlay = iota
	ovSwitch
	ovHelp
)

type message struct {
	id, sender, name, text, kind, err string
	at                                time.Time
	own, pending, failed, edited      bool
	deleted                           bool
}

type pendingCmd struct {
	kind string
	msg  *message
	arg  string
}

// entry is one row of the channel list.
type entry struct {
	guild *proto.Guild
	ch    proto.Channel
}

func (e entry) label() string {
	if e.guild.DM {
		return "@" + e.guild.Name
	}
	return e.guild.Name + " / #" + e.ch.Name
}

// Model is the Bubble Tea model.
type Model struct {
	bot   Sender
	trust *trust.Store
	opts  Options
	th    theme

	selfFP, selfName string
	guilds           []proto.Guild
	active           string
	msgs             map[string][]*message
	unread           map[string]int
	members          map[string][]proto.Member
	pending          map[uint64]pendingCmd

	w, h        int
	in          editor
	hist        []string
	histPos     int
	ov          overlay
	showMembers bool
	sw          editor
	swSel       int
	scroll      int
	notice      string
	noticeBad   bool

	// Err is why the connection ended, if it did.
	Err error
}

// New builds the model from the handshake.
func New(b Sender, t *trust.Store, ready proto.Event, o Options) *Model {
	m := &Model{
		bot: b, trust: t, opts: o, th: newTheme(true, o.Plain),
		selfFP: ready.Fingerprint, selfName: ready.Name,
		msgs: map[string][]*message{}, unread: map[string]int{},
		members: map[string][]proto.Member{}, pending: map[uint64]pendingCmd{},
	}
	m.setGuilds(ready.Guilds)
	m.request(proto.Command{Cmd: "list"})
	m.say(false, "unlocked · ctrl-k to switch · /help")
	return m
}

func (m *Model) Init() tea.Cmd {
	if m.opts.Plain {
		return nil
	}
	return tea.RequestBackgroundColor
}

func (m *Model) say(bad bool, format string, a ...any) {
	m.notice, m.noticeBad = fmt.Sprintf(format, a...), bad
}

func (m *Model) request(c proto.Command) uint64 {
	id, err := m.bot.Send(c)
	if err != nil {
		m.say(true, "%s", safe.Line(err.Error()))
		return 0
	}
	return id
}

// ---- channels ----

func visible(c proto.Channel) bool {
	switch c.Type {
	case "", "text", "announcement":
		return true
	}
	return false
}

func (m *Model) entries() []entry {
	var rooms, dms []entry
	for i := range m.guilds {
		g := &m.guilds[i]
		for _, c := range g.Channels {
			if !visible(c) {
				continue
			}
			if g.DM {
				dms = append(dms, entry{g, c})
			} else {
				rooms = append(rooms, entry{g, c})
			}
		}
	}
	return append(rooms, dms...)
}

func (m *Model) find(ch string) (entry, bool) {
	for _, e := range m.entries() {
		if e.ch.ID == ch {
			return e, true
		}
	}
	return entry{}, false
}

func (m *Model) setGuilds(gs []proto.Guild) {
	m.guilds = gs
	if _, ok := m.find(m.active); !ok {
		m.active = ""
		if es := m.entries(); len(es) > 0 {
			m.switchTo(es[0].ch.ID)
		}
	}
	for _, g := range gs {
		if _, ok := m.members[g.ID]; !ok {
			m.members[g.ID] = nil
			m.request(proto.Command{Cmd: "members", Guild: g.ID})
		}
	}
}

func (m *Model) switchTo(ch string) {
	m.active = ch
	m.unread[ch] = 0
	m.scroll = 0
}

func (m *Model) move(delta int) {
	es := m.entries()
	if len(es) == 0 {
		return
	}
	i := slices.IndexFunc(es, func(e entry) bool { return e.ch.ID == m.active })
	m.switchTo(es[((i+delta)%len(es)+len(es))%len(es)].ch.ID)
}

// matches ranks channels for the switcher: name prefix, then substring,
// then subsequence; unread first among equals.
func (m *Model) matches(q string) []entry {
	q = strings.ToLower(strings.TrimSpace(q))
	type scored struct {
		e     entry
		score int
		i     int
	}
	var out []scored
	for i, e := range m.entries() {
		name := strings.ToLower(e.ch.Name)
		if e.guild.DM {
			name = strings.ToLower(e.guild.Name)
		}
		label := strings.ToLower(e.label())
		s := 0
		switch {
		case q == "":
			s = 1
		case strings.HasPrefix(name, q):
			s = 4
		case strings.Contains(name, q):
			s = 3
		case strings.Contains(label, q):
			s = 2
		case subsequence(label, q):
			s = 1
		}
		if s > 0 {
			out = append(out, scored{e, s, i})
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].score != out[b].score {
			return out[a].score > out[b].score
		}
		ua, ub := m.unread[out[a].e.ch.ID] > 0, m.unread[out[b].e.ch.ID] > 0
		if ua != ub {
			return ua
		}
		return out[a].i < out[b].i
	})
	es := make([]entry, len(out))
	for i, s := range out {
		es[i] = s.e
	}
	return es
}

func subsequence(s, q string) bool {
	rs := []rune(s)
	i := 0
	for _, r := range q {
		for i < len(rs) && rs[i] != r {
			i++
		}
		if i == len(rs) {
			return false
		}
		i++
	}
	return true
}

// ---- members and trust ----

// known maps fingerprints to names, the active guild's members first.
func (m *Model) known() []proto.Member {
	seen := map[string]bool{trust.Norm(m.selfFP): true}
	var out []proto.Member
	add := func(ms []proto.Member) {
		for _, x := range ms {
			if n := trust.Norm(x.Fingerprint); !seen[n] {
				seen[n] = true
				out = append(out, x)
			}
		}
	}
	if e, ok := m.find(m.active); ok {
		add(m.members[e.guild.ID])
	}
	ids := make([]string, 0, len(m.members))
	for id := range m.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		add(m.members[id])
	}
	return out
}

func (m *Model) lookup(who string) []proto.Member {
	var byName, byFP []proto.Member
	n := trust.Norm(who)
	for _, x := range m.known() {
		if strings.EqualFold(x.Name, strings.TrimSpace(who)) {
			byName = append(byName, x)
		} else if len(n) >= 4 && strings.HasPrefix(trust.Norm(x.Fingerprint), n) {
			byFP = append(byFP, x)
		}
	}
	if len(byName) > 0 {
		return byName
	}
	return byFP
}

func shortFP(fp string) string {
	n := trust.Norm(fp)
	if len(n) < 4 {
		return n
	}
	return n[:4]
}

func (m *Model) verify(arg string) {
	if arg == "" {
		m.say(false, "/verify <name> shows a fingerprint · /verify <fingerprint> marks it verified")
		return
	}
	if len(trust.Norm(arg)) >= 32 {
		for _, x := range m.known() {
			if trust.MatchFull(x.Fingerprint, arg) {
				if err := m.trust.Mark(x.Fingerprint, x.Name); err != nil {
					m.say(true, "could not save: %v", err)
					return
				}
				m.say(false, "verified %s", x.Name)
				return
			}
		}
		m.say(true, "no member has that fingerprint; it does not match anyone here")
		return
	}
	ms := m.lookup(arg)
	switch len(ms) {
	case 0:
		m.say(true, "no member matches %q", safe.Line(arg))
	case 1:
		m.say(false, "%s  %s  · compare with them, then /verify <the fingerprint they read out>", ms[0].Name, ms[0].Fingerprint)
	default:
		var names []string
		for _, x := range ms {
			names = append(names, x.Name+"·"+shortFP(x.Fingerprint))
		}
		m.say(true, "several match: %s", strings.Join(names, ", "))
	}
}

func (m *Model) unverify(arg string) {
	ms := m.lookup(arg)
	if len(trust.Norm(arg)) >= 32 {
		ms = nil
		for _, x := range m.known() {
			if trust.MatchFull(x.Fingerprint, arg) {
				ms = append(ms, x)
			}
		}
	}
	if len(ms) != 1 {
		m.say(true, "name exactly one member: /unverify <name or fingerprint>")
		return
	}
	if err := m.trust.Unmark(ms[0].Fingerprint); err != nil {
		m.say(true, "could not save: %v", err)
		return
	}
	m.say(false, "%s is no longer marked verified", ms[0].Name)
}

// displayName adds a fingerprint tag when the name is not unique in the
// guild, or when someone else uses the user's own name: names are
// self-chosen and only fingerprints identify.
func (m *Model) displayName(guildID string, msg *message) string {
	if msg.own {
		return m.selfName
	}
	clash := strings.EqualFold(msg.name, m.selfName)
	for _, x := range m.members[guildID] {
		if strings.EqualFold(x.Name, msg.name) && trust.Norm(x.Fingerprint) != trust.Norm(msg.sender) {
			clash = true
		}
	}
	if clash {
		return msg.name + "·" + shortFP(msg.sender)
	}
	return msg.name
}

// ---- update ----

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.th = newTheme(msg.IsDark(), m.opts.Plain)
	case EventMsg:
		m.onEvent(msg.Event)
	case ExitMsg:
		m.Err = msg.Err
		return m, tea.Quit
	case tea.PasteMsg:
		if m.ov == ovSwitch {
			m.sw.insert(msg.Content)
		} else {
			m.in.insert(msg.Content)
		}
	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m *Model) onKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if m.ov == ovSwitch {
		switch key {
		case "esc", "ctrl+k", "ctrl+c":
			m.ov = ovNone
		case "enter":
			if es := m.matches(m.sw.String()); m.swSel < len(es) {
				m.switchTo(es[m.swSel].ch.ID)
			}
			m.ov = ovNone
		case "up", "ctrl+p", "shift+tab":
			m.swSel = max(0, m.swSel-1)
		case "down", "ctrl+n", "tab":
			m.swSel = min(max(0, len(m.matches(m.sw.String()))-1), m.swSel+1)
		default:
			if m.sw.key(k) {
				m.swSel = 0
			}
		}
		return m, nil
	}
	if m.ov == ovHelp {
		m.ov = ovNone
		if key == "esc" || key == "enter" || key == "q" {
			return m, nil
		}
	}
	switch key {
	case "ctrl+c":
		if !m.in.empty() {
			m.in.set("")
			return m, nil
		}
		return m, tea.Quit
	case "ctrl+d":
		if m.in.empty() {
			return m, tea.Quit
		}
		m.in.key(k)
	case "ctrl+k":
		m.ov, m.swSel = ovSwitch, 0
		m.sw.set("")
	case "tab":
		m.showMembers = !m.showMembers
	case "esc":
		m.notice = ""
	case "enter":
		return m, m.submit()
	case "pgup":
		m.scroll += max(1, m.bodyH()-2)
	case "pgdown":
		m.scroll = max(0, m.scroll-max(1, m.bodyH()-2))
	case "alt+up", "ctrl+up":
		m.move(-1)
	case "alt+down", "ctrl+down":
		m.move(1)
	case "up":
		if m.histPos > 0 {
			m.histPos--
			m.in.set(m.hist[m.histPos])
		}
	case "down":
		if m.histPos < len(m.hist) {
			m.histPos++
			if m.histPos == len(m.hist) {
				m.in.set("")
			} else {
				m.in.set(m.hist[m.histPos])
			}
		}
	default:
		m.in.key(k)
	}
	return m, nil
}

func (m *Model) submit() tea.Cmd {
	line := strings.TrimSpace(m.in.String())
	if line == "" {
		return nil
	}
	m.in.set("")
	m.hist = append(m.hist, line)
	if len(m.hist) > 200 {
		m.hist = m.hist[1:]
	}
	m.histPos = len(m.hist)
	switch {
	case strings.HasPrefix(line, "//"):
		m.send(line[1:])
	case strings.HasPrefix(line, "/"):
		return m.command(line)
	default:
		m.send(line)
	}
	return nil
}

func (m *Model) send(text string) {
	if m.active == "" {
		m.say(true, "no channel yet · /join <invite>")
		return
	}
	m.scroll = 0
	msg := &message{sender: m.selfFP, name: m.selfName, text: safe.Text(text), at: time.Now(), own: true, pending: true}
	id, err := m.bot.Send(proto.Command{Cmd: "send", Channel: m.active, Text: text})
	if err != nil {
		msg.pending, msg.failed, msg.err = false, true, safe.Line(err.Error())
	} else {
		m.pending[id] = pendingCmd{kind: "send", msg: msg}
	}
	m.add(m.active, msg)
}

func (m *Model) command(line string) tea.Cmd {
	name, arg, _ := strings.Cut(line, " ")
	arg = strings.TrimSpace(arg)
	switch strings.ToLower(name) {
	case "/help", "/?":
		m.ov = ovHelp
	case "/quit", "/q", "/exit", "/detach":
		return tea.Quit
	case "/shutdown":
		if !m.opts.Remote {
			m.say(false, "/shutdown stops an attached host; locally, /quit")
			return nil
		}
		m.request(proto.Command{Cmd: "host-stop"})
		return tea.Quit
	case "/join":
		if arg == "" {
			m.say(true, "/join <invite code>")
			return nil
		}
		if id := m.request(proto.Command{Cmd: "join", Invite: arg}); id != 0 {
			m.pending[id] = pendingCmd{kind: "join"}
			m.say(false, "joining · someone who can admit you must be online")
		}
	case "/invite", "/create":
		// Protocol v1 has no command to create either.
		m.say(false, "create in the Concord app · then /join <code>")
	case "/members", "/m":
		m.showMembers = !m.showMembers
	case "/verify":
		m.verify(arg)
	case "/unverify":
		m.unverify(arg)
	case "/whoami", "/me":
		m.say(false, "%s  %s", m.selfName, m.selfFP)
	case "/name", "/nick":
		if arg == "" {
			m.say(true, "/name <display name>")
			return nil
		}
		if id := m.request(proto.Command{Cmd: "name", Text: arg}); id != 0 {
			m.pending[id] = pendingCmd{kind: "name", arg: safe.Name(arg)}
		}
	case "/go", "/switch":
		es := m.matches(arg)
		if arg != "" && len(es) > 0 {
			m.switchTo(es[0].ch.ID)
			return nil
		}
		m.ov, m.swSel = ovSwitch, 0
		m.sw.set(arg)
	default:
		m.say(true, "unknown command %s · /help", safe.Line(name))
	}
	return nil
}

func (m *Model) add(ch string, msg *message) {
	list := m.msgs[ch]
	if msg.id != "" {
		for _, x := range list {
			if x.id == msg.id {
				return
			}
		}
	}
	list = append(list, msg)
	if len(list) > maxPerChannel {
		list = list[len(list)-maxPerChannel:]
	}
	m.msgs[ch] = list
}

func (m *Model) byID(ch, id string) *message {
	for _, x := range m.msgs[ch] {
		if x.id == id {
			return x
		}
	}
	return nil
}

func (m *Model) onEvent(e proto.Event) {
	id := e.ReqID()
	p, isReply := m.pending[id]
	if isReply {
		delete(m.pending, id)
	}
	switch e.Type {
	case "message":
		at, err := time.Parse(time.RFC3339Nano, e.Sent)
		if err != nil {
			at = time.Now()
		}
		m.add(e.ChannelID, &message{id: e.MessageID, sender: e.Sender, name: e.SenderName, text: e.Content, kind: e.Kind, at: at})
		if e.ChannelID != m.active && e.Kind == "" {
			m.unread[e.ChannelID]++
		}
		// Names change without an event; a message carries the current one
		// next to the authenticated fingerprint.
		for i, x := range m.members[e.GuildID] {
			if trust.Norm(x.Fingerprint) == trust.Norm(e.Sender) && e.SenderName != "" {
				m.members[e.GuildID][i].Name = e.SenderName
			}
		}
		if _, ok := m.find(e.ChannelID); !ok {
			m.request(proto.Command{Cmd: "list"})
		}
	case "edit":
		if x := m.byID(e.ChannelID, e.MessageID); x != nil {
			x.text, x.edited = e.Content, true
		}
	case "delete":
		if x := m.byID(e.ChannelID, e.MessageID); x != nil {
			x.deleted = true
		}
	case "member":
		list := m.members[e.GuildID]
		list = slices.DeleteFunc(list, func(x proto.Member) bool { return x.Fingerprint == e.Fingerprint })
		verb := "left"
		if e.Action == "join" {
			list = append(list, proto.Member{Fingerprint: e.Fingerprint, Name: e.Name})
			verb = "joined"
		}
		m.members[e.GuildID] = list
		m.say(false, "%s %s %s", e.Name, verb, e.GuildName)
	case "guild":
		if e.Action == "leave" {
			delete(m.members, e.GuildID)
			m.say(false, "left %s", e.GuildName)
		}
		m.request(proto.Command{Cmd: "list"})
	case "guilds":
		m.setGuilds(e.Guilds)
	case "members":
		m.members[e.GuildID] = e.Members
	case "joined":
		if e.Joined != nil {
			gs := slices.DeleteFunc(slices.Clone(m.guilds), func(g proto.Guild) bool { return g.ID == e.Joined.ID })
			m.setGuilds(append(gs, *e.Joined))
			for _, c := range e.Joined.Channels {
				if visible(c) {
					m.switchTo(c.ID)
					break
				}
			}
			m.say(false, "joined %s", e.Joined.Name)
		}
		m.request(proto.Command{Cmd: "list"})
	case "sent":
		if isReply && p.msg != nil {
			p.msg.pending, p.msg.id = false, e.MessageID
		}
	case "ok":
		if isReply && p.kind == "name" {
			m.selfName = p.arg
			m.say(false, "you are now %s", p.arg)
		}
	case "error":
		if isReply && p.msg != nil {
			p.msg.pending, p.msg.failed, p.msg.err = false, true, e.Error
			return
		}
		m.say(true, "%s", e.Error)
	case "client-error":
		m.say(true, "%s", e.Error)
	}
}

func (m *Model) bodyH() int { return max(0, m.h-3) }

// Active returns the guild and channel ids on screen.
func (m *Model) Active() (guildID, channelID string) {
	if e, ok := m.find(m.active); ok {
		return e.guild.ID, e.ch.ID
	}
	return "", m.active
}
