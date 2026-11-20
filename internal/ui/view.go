package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZahakJ/concord-cli/internal/trust"
)

// fit pads or cuts a (possibly styled) string to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		return ansi.Truncate(s, w, "")
	}
	return s + strings.Repeat(" ", w-sw)
}

// cut shortens plain text to w cells with an ellipsis.
func cut(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

type layout struct {
	sideW, mainX, mainW, memW, bodyW, bodyH int
}

func (m *Model) layout() layout {
	l := layout{}
	switch {
	case m.w >= 100:
		l.sideW = 26
	case m.w >= 60:
		l.sideW = 22
	}
	if l.sideW > 0 {
		l.mainX = l.sideW + 1
	}
	l.mainW = m.w - l.mainX
	if m.showMembers && l.mainW >= 90 {
		l.memW = 34
	}
	l.bodyW = l.mainW
	if l.memW > 0 {
		l.bodyW = l.mainW - l.memW - 1
	}
	l.bodyH = m.bodyH()
	return l
}

// View renders the screen.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "concord"
	if n := m.totalUnread(); n > 0 {
		v.WindowTitle = fmt.Sprintf("concord (%d)", n)
	}
	if m.w >= 20 && m.h >= 5 {
		l := m.layout()
		if m.ov == ovSwitch {
			_, x := m.sw.view(max(1, l.mainW-6))
			v.Cursor = tea.NewCursor(l.mainX+4+x, 2)
		} else {
			_, x := m.in.view(max(1, l.mainW-2))
			v.Cursor = tea.NewCursor(l.mainX+2+x, m.h-1)
		}
	}
	return v
}

func (m *Model) totalUnread() int {
	n := 0
	for _, c := range m.unread {
		n += c
	}
	return n
}

func (m *Model) render() string {
	if m.w <= 0 || m.h <= 0 {
		return ""
	}
	if m.w < 20 || m.h < 5 {
		rows := make([]string, m.h)
		rows[0] = fit("window too small", m.w)
		for i := 1; i < m.h; i++ {
			rows[i] = fit("", m.w)
		}
		return strings.Join(rows, "\n")
	}
	l := m.layout()
	side := m.sidebar(l)

	var body []string
	switch {
	case m.ov == ovSwitch:
		body = m.switcher(l.mainW, l.bodyH)
	case m.ov == ovHelp:
		body = m.help(l.mainW, l.bodyH)
	case m.showMembers && l.memW == 0:
		body = m.membersPane(l.mainW, l.bodyH)
	default:
		body = m.messages(l.bodyW, l.bodyH)
		if l.memW > 0 {
			pane := m.membersPane(l.memW, l.bodyH)
			for i := range body {
				body[i] = fit(body[i], l.bodyW) + m.th.dim("│") + fit(pane[i], l.memW)
			}
		}
	}

	main := make([]string, 0, m.h)
	main = append(main, m.header(l.mainW))
	main = append(main, body...)
	main = append(main, m.rule(l.mainW), m.input(l.mainW))

	rows := make([]string, m.h)
	for i := range rows {
		row := ""
		if l.sideW > 0 {
			row = fit(side[i], l.sideW) + m.th.dim("│")
		}
		rows[i] = fit(row+fit(main[i], l.mainW), m.w)
	}
	return strings.Join(rows, "\n")
}

// ---- sidebar ----

func (m *Model) sidebar(l layout) []string {
	w := l.sideW
	out := make([]string, m.h)
	if w == 0 {
		return out
	}
	type row struct {
		s      string
		active bool
	}
	var list []row
	lastGuild, dmHeader := "", false
	for _, e := range m.entries() {
		if e.guild.DM {
			if !dmHeader {
				list = append(list, row{s: ""}, row{s: " " + m.th.dim(cut("direct", w-2))})
				dmHeader = true
			}
		} else if e.guild.ID != lastGuild {
			if lastGuild != "" {
				list = append(list, row{s: ""})
			}
			list = append(list, row{s: " " + m.th.dim(cut(e.guild.Name, w-2))})
			lastGuild = e.guild.ID
		}
		active := e.ch.ID == m.active
		n := m.unread[e.ch.ID]
		count := ""
		if n > 0 {
			count = fmt.Sprint(n)
			if n > 99 {
				count = "99+"
			}
		}
		sigil, name := "# ", e.ch.Name
		if e.guild.DM {
			sigil, name = "@ ", e.guild.Name
		}
		nameW := w - 4 - len(count) - 1
		label := fit(sigil+cut(name, nameW-2), nameW)
		bar := " "
		switch {
		case active:
			bar, label = m.th.accent("▌"), m.th.accentBold(label)
		case n > 0:
			label = m.th.bold(label)
		default:
			label = m.th.dim(label)
		}
		list = append(list, row{s: bar + " " + label + " " + m.th.accent(count), active: active})
	}

	out[0] = " " + m.th.accentBold("concord")
	if m.opts.Remote {
		out[0] += m.th.dim(" · attached")
	}
	top, bottom := 2, 2
	avail := m.h - top - bottom - 1
	start := 0
	if len(list) > avail && avail > 0 {
		at := 0
		for i, r := range list {
			if r.active {
				at = i
			}
		}
		start = max(0, min(at-avail/2, len(list)-avail))
	}
	for i := 0; i < avail && start+i < len(list); i++ {
		out[top+i] = list[start+i].s
	}
	if len(list) == 0 && avail > 1 {
		out[top] = " " + m.th.dim(cut("no guilds yet", w-2))
		out[top+1] = " " + m.th.dim(cut("/join <invite>", w-2))
	}
	out[m.h-2] = " " + cut(m.selfName, w-2)
	out[m.h-1] = " " + m.th.dim(cut(groups(m.selfFP, 2), w-2))
	return out
}

// groups returns the first n four-character groups of a fingerprint.
func groups(fp string, n int) string {
	f := strings.Fields(fp)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

// ---- header, rule, input ----

func (m *Model) header(w int) string {
	e, ok := m.find(m.active)
	if !ok {
		return fit(" "+m.th.dim("no channel"), w)
	}
	left := " " + m.th.bold("# "+e.ch.Name) + "  " + m.th.dim(e.guild.Name)
	if e.guild.DM {
		left = " " + m.th.bold("@ "+e.guild.Name)
	}
	total, verified := 0, 0
	for _, x := range m.members[e.guild.ID] {
		total++
		if trust.Norm(x.Fingerprint) != trust.Norm(m.selfFP) && m.trust.Verified(x.Fingerprint) {
			verified++
		}
	}
	right := ""
	if total > 0 {
		right = m.th.dim(fmt.Sprintf("%d members · %d verified ", total, verified))
	}
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 2 {
		return fit(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) rule(w int) string {
	right := ""
	if m.scroll > 0 {
		right = " pgdn ↓ "
	}
	if m.notice == "" {
		return m.th.dim(fit(strings.Repeat("─", max(0, w-len([]rune(right))))+right, w))
	}
	note := cut(m.notice, max(1, w-4-ansi.StringWidth(right)))
	styled := m.th.dim(note)
	if m.noticeBad {
		styled = m.th.bad(note)
	}
	fill := max(0, w-3-ansi.StringWidth(note)-ansi.StringWidth(right))
	return m.th.dim("─ ") + styled + " " + m.th.dim(strings.Repeat("─", fill)+right)
}

func (m *Model) input(w int) string {
	prompt := m.th.accent("› ")
	if m.in.empty() {
		hint := "message"
		if e, ok := m.find(m.active); ok {
			if e.guild.DM {
				hint += " @" + e.guild.Name
			} else {
				hint += " #" + e.ch.Name
			}
		}
		return prompt + m.th.dim(cut(hint, w-2))
	}
	text, _ := m.in.view(max(1, w-2))
	return prompt + text
}

// ---- messages ----

func (m *Model) messages(w, h int) []string {
	out := make([]string, h)
	list := m.msgs[m.active]
	if len(list) == 0 {
		if h >= 3 {
			mid := h / 2
			out[mid-1] = center(m.th.dim("No messages yet."), w)
			out[mid] = center(m.th.dim(cut("concord --bot shows what arrives from now on.", w)), w)
		}
		return out
	}
	guildID := ""
	if e, ok := m.find(m.active); ok {
		guildID = e.guild.ID
	}
	var lines []string
	lastDay := ""
	for _, msg := range list {
		day := msg.at.Local().Format("Mon 2 Jan")
		if day != lastDay {
			lastDay = day
			label := " " + day + " "
			side := max(0, (w-ansi.StringWidth(label))/2)
			lines = append(lines, m.th.dim(fit(strings.Repeat("─", side)+label+strings.Repeat("─", side), w)))
		}
		lines = append(lines, m.renderMessage(guildID, msg, w)...)
	}
	m.scroll = min(m.scroll, max(0, len(lines)-h))
	end := len(lines) - m.scroll
	start := max(0, end-h)
	copy(out[h-(end-start):], lines[start:end])
	return out
}

func center(s string, w int) string {
	pad := max(0, (w-ansi.StringWidth(s))/2)
	return strings.Repeat(" ", pad) + s
}

func (m *Model) renderMessage(guildID string, msg *message, w int) []string {
	ts := m.th.dim(msg.at.Local().Format("15:04"))
	if msg.kind == "system" {
		return wrapInto(m.th.italic, ts+" ", 6, msg.name+" "+msg.text, w)
	}
	name := cut(m.displayName(guildID, msg), 18)
	nameW := ansi.StringWidth(name)
	styledName := m.th.bold(name)
	if msg.own {
		styledName = m.th.accentBold(name)
	}
	mark := ""
	if !msg.own && m.trust.Verified(msg.sender) {
		mark = " " + m.th.good("✓")
		nameW += 2
	}
	text, style := msg.text, func(s string) string { return s }
	switch {
	case msg.deleted:
		text, style = "message deleted", m.th.italic
	case msg.failed:
		text, style = text+"  · not sent: "+msg.err, m.th.bad
	case msg.pending:
		text, style = text+"  · sending", m.th.dim
	case msg.edited:
		text += "  (edited)"
	}
	prefix := ts + " " + styledName + mark + "  "
	indent := 6 + nameW + 2
	if w-indent < 16 { // narrow: the text goes under the name
		return append([]string{fit(prefix, w)}, wrapInto(style, "  ", 2, text, w)...)
	}
	return wrapInto(style, prefix, indent, text, w)
}

// wrapInto wraps text to the width left after indent, puts prefix before
// the first line and spaces before the rest, and styles the text.
func wrapInto(style func(string) string, prefix string, indent int, text string, w int) []string {
	tw := max(1, w-indent)
	var out []string
	for i, l := range strings.Split(ansi.Wrap(text, tw, ""), "\n") {
		lead := strings.Repeat(" ", indent)
		if i == 0 {
			lead = prefix
		}
		out = append(out, fit(lead+style(l), w))
	}
	return out
}

// ---- panes ----

func (m *Model) membersPane(w, h int) []string {
	out := make([]string, h)
	e, ok := m.find(m.active)
	if !ok {
		return out
	}
	lines := []string{" " + m.th.dim(cut("members · "+e.guild.Name, w-1)), ""}
	per := max(1, (w-4+1)/5) // fingerprint groups per line
	for _, x := range m.members[e.guild.ID] {
		self := trust.Norm(x.Fingerprint) == trust.Norm(m.selfFP)
		mark, name := "  ", cut(x.Name, w-4)
		switch {
		case self:
			name = cut(m.selfName, w-10) + m.th.dim(" (you)")
		case m.trust.Verified(x.Fingerprint):
			mark = m.th.good("✓") + " "
		}
		lines = append(lines, " "+mark+name)
		f := strings.Fields(x.Fingerprint)
		for i := 0; i < len(f); i += per {
			lines = append(lines, "   "+m.th.dim(strings.Join(f[i:min(len(f), i+per)], " ")))
		}
	}
	lines = append(lines, "", " "+m.th.dim(cut("✓ = fingerprint compared in person", w-1)), " "+m.th.dim(cut("/verify <name> to compare", w-1)))
	for i := 0; i < h && i < len(lines); i++ {
		out[i] = fit(lines[i], w)
	}
	return out
}

func (m *Model) switcher(w, h int) []string {
	out := make([]string, h)
	if h < 3 {
		return out
	}
	q, _ := m.sw.view(max(1, w-6))
	out[1] = "  " + m.th.accent("› ") + q
	out[2] = "  " + m.th.dim(strings.Repeat("─", max(0, min(w-4, 48))))
	es := m.matches(m.sw.String())
	rows := h - 4
	start := max(0, m.swSel-rows+1)
	for i := 0; i < rows && start+i < len(es); i++ {
		e := es[start+i]
		count := ""
		if n := m.unread[e.ch.ID]; n > 0 {
			count = fmt.Sprint(n)
		}
		label := fit(cut(e.label(), min(w-4, 48)-len(count)-2), min(w-4, 48)-len(count)-1)
		if start+i == m.swSel {
			out[3+i] = m.th.accent("  ▌") + m.th.accentBold(label) + " " + m.th.accent(count)
		} else {
			out[3+i] = "   " + label + " " + m.th.accent(count)
		}
	}
	if len(es) == 0 {
		out[3] = "   " + m.th.dim("no match")
	}
	return out
}

func (m *Model) help(w, h int) []string {
	rows := [][2]string{
		{"ctrl-k", "switch channel"},
		{"alt-↑ alt-↓", "previous / next channel"},
		{"tab", "members and fingerprints"},
		{"pgup pgdn", "scroll"},
		{"ctrl-c", "clear the line, then quit"},
		{"", ""},
		{"/join <code>", "join a guild by invite"},
		{"/verify <name>", "show a fingerprint to compare"},
		{"/verify <fp>", "mark it verified"},
		{"/unverify <name>", "forget a verification"},
		{"/name <name>", "set your display name"},
		{"/whoami", "your fingerprint"},
		{"/go <query>", "switch by name"},
		{"//text", "send text starting with /"},
		{"/quit", "leave (detach when attached)"},
	}
	if m.opts.Remote {
		rows = append(rows, [2]string{"/shutdown", "stop the host's concord"})
	}
	out := make([]string, h)
	for i, r := range rows {
		if i+1 >= h {
			break
		}
		out[i+1] = fit("  "+m.th.accent(fit(r[0], 18))+m.th.dim(r[1]), w)
	}
	return out
}
