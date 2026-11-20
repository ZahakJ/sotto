// Package proto is the client side of the line protocol spoken by
// `concord --bot` (docs/BOTS.md in the Concord repository): one JSON object
// per line, commands on the child's stdin, events on its stdout.
//
// Decoding is also the trust boundary. Every string a peer can influence is
// cleaned by package safe here, so the rest of the client never holds raw
// remote text.
package proto

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/ZahakJ/concord-cli/internal/safe"
)

// Version is the protocol revision this client understands: the one
// documented in docs/BOTS.md since Concord v0.59.0, which carries no version
// field of its own. A `ready` event that announces a higher "protocol" is
// refused, so a future, incompatible concord fails loudly instead of oddly.
const Version = 1

// MinRelease is the first Concord release with `concord --bot`.
const MinRelease = "v0.59.0"

// Channel is one channel of a guild.
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	Ref  string `json:"ref"`
}

// Guild is a guild or a direct conversation (DM).
type Guild struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	DM       bool      `json:"dm,omitempty"`
	Channels []Channel `json:"channels"`
}

// Member is one account in a guild.
type Member struct {
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
}

// Event is any line the bot writes. Fields not used by a Type are zero.
type Event struct {
	Type string          `json:"type"`
	ID   json.RawMessage `json:"id,omitempty"`

	// ready
	Protocol    int    `json:"protocol,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	PeerID      string `json:"peerId,omitempty"`
	Name        string `json:"name,omitempty"`

	// message, edit, delete, member, guild, sent, members
	GuildID    string `json:"guildId,omitempty"`
	ChannelID  string `json:"channelId,omitempty"`
	Channel    string `json:"channel,omitempty"`
	MessageID  string `json:"messageId,omitempty"`
	Sender     string `json:"sender,omitempty"`
	SenderName string `json:"senderName,omitempty"`
	Content    string `json:"content,omitempty"`
	Sent       string `json:"sent,omitempty"`
	ReplyTo    string `json:"replyTo,omitempty"`
	Kind       string `json:"kind,omitempty"`
	DM         bool   `json:"dm,omitempty"`
	Action     string `json:"action,omitempty"`
	Cmd        string `json:"cmd,omitempty"`
	Error      string `json:"error,omitempty"`

	// guild is a name in member/guild events and an object in `joined`;
	// GuildRaw holds whichever arrived and Decode sorts it out.
	GuildRaw  json.RawMessage `json:"guild,omitempty"`
	GuildName string          `json:"-"`
	Joined    *Guild          `json:"-"`

	Guilds  []Guild  `json:"guilds,omitempty"`
	Members []Member `json:"members,omitempty"`
}

// Decode parses one stdout line and cleans every remote string in it.
func Decode(line []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(line, &e); err != nil {
		return Event{}, fmt.Errorf("unreadable line from concord: %w", err)
	}
	if e.Type == "" {
		return Event{}, errors.New(`line from concord has no "type"`)
	}
	if len(e.GuildRaw) > 0 {
		var name string
		if json.Unmarshal(e.GuildRaw, &name) == nil {
			e.GuildName = name
		} else {
			var g Guild
			if err := json.Unmarshal(e.GuildRaw, &g); err != nil {
				return Event{}, fmt.Errorf("unreadable guild in %q event", e.Type)
			}
			e.Joined = &g
		}
		e.GuildRaw = nil
	}
	e.clean()
	return e, nil
}

func (e *Event) clean() {
	line := safe.Line
	e.Type, e.Fingerprint, e.PeerID = line(e.Type), line(e.Fingerprint), line(e.PeerID)
	e.GuildID, e.ChannelID, e.MessageID = line(e.GuildID), line(e.ChannelID), line(e.MessageID)
	e.Sender, e.Sent, e.ReplyTo = line(e.Sender), line(e.Sent), line(e.ReplyTo)
	e.Kind, e.Action, e.Cmd, e.Error = line(e.Kind), line(e.Action), line(e.Cmd), line(e.Error)
	if e.Name != "" {
		e.Name = safe.Name(e.Name)
	}
	if e.SenderName != "" || e.Type == "message" {
		e.SenderName = safe.Name(e.SenderName)
	}
	if e.Channel != "" {
		e.Channel = safe.Name(e.Channel)
	}
	if e.GuildName != "" {
		e.GuildName = safe.Name(e.GuildName)
	}
	e.Content = safe.Text(e.Content)
	if e.Joined != nil {
		e.Joined.clean()
	}
	for i := range e.Guilds {
		e.Guilds[i].clean()
	}
	for i := range e.Members {
		e.Members[i].Fingerprint = line(e.Members[i].Fingerprint)
		e.Members[i].Name = safe.Name(e.Members[i].Name)
	}
	// The id is our own JSON echoed back; keep it only if it is a number,
	// which is all this client ever sends.
	if len(e.ID) > 0 {
		if _, err := strconv.ParseUint(string(e.ID), 10, 64); err != nil {
			e.ID = nil
		}
	}
}

func (g *Guild) clean() {
	g.ID, g.Name = safe.Line(g.ID), safe.Name(g.Name)
	for i := range g.Channels {
		c := &g.Channels[i]
		c.ID, c.Name, c.Type, c.Ref = safe.Line(c.ID), safe.Name(c.Name), safe.Line(c.Type), safe.Line(c.Ref)
	}
}

// ReqID returns the request id a reply echoes, or 0.
func (e Event) ReqID() uint64 {
	n, _ := strconv.ParseUint(string(e.ID), 10, 64)
	return n
}

// Command is one stdin line. Only the fields a command needs are set.
type Command struct {
	ID      uint64 `json:"id,omitempty"`
	Cmd     string `json:"cmd"`
	Channel string `json:"channel,omitempty"`
	Text    string `json:"text,omitempty"`
	ReplyTo string `json:"replyTo,omitempty"`
	Message string `json:"message,omitempty"`
	Emoji   string `json:"emoji,omitempty"`
	Invite  string `json:"invite,omitempty"`
	Guild   string `json:"guild,omitempty"`
}

// Encode renders a command as one line, newline included. JSON escapes any
// newline inside a string, so a message can never split into two commands.
func Encode(c Command) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// EncodeEvent renders an event as one line, for a host relaying it to an
// attached client. The client decodes and cleans it again.
func EncodeEvent(e Event) ([]byte, error) {
	switch {
	case e.Joined != nil:
		b, err := json.Marshal(e.Joined)
		if err != nil {
			return nil, err
		}
		e.GuildRaw = b
	case e.GuildName != "":
		b, _ := json.Marshal(e.GuildName)
		e.GuildRaw = b
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// CheckReady validates the handshake: the first event must be `ready`, with
// the fields this client relies on, at a protocol revision it understands.
func CheckReady(e Event) error {
	if e.Type != "ready" {
		return fmt.Errorf("concord spoke %q before \"ready\"; it is not a protocol v%d bot", e.Type, Version)
	}
	if e.Fingerprint == "" || e.PeerID == "" {
		return fmt.Errorf("concord's \"ready\" lacks fingerprint or peerId; it is not a protocol v%d bot", Version)
	}
	if e.Protocol > Version {
		return fmt.Errorf("concord speaks protocol v%d; this client understands v%d (upgrade concord-cli)", e.Protocol, Version)
	}
	return nil
}

var versionRE = regexp.MustCompile(`^concord (v?\d+\.\d+\.\d+\S*|dev)\b`)

// ParseVersion extracts the release from `concord --version` output. A
// development build reports "dev", which has no number to compare.
func ParseVersion(out string) (string, error) {
	m := versionRE.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("unrecognised `concord --version` output %q", safe.Line(firstLine(out)))
	}
	return m[1], nil
}

// TooOld reports whether a release predates MinRelease. "dev" is never too
// old by number; the handshake still has to pass.
func TooOld(v string) bool {
	if v == "dev" {
		return false
	}
	return compareSemver(v, MinRelease) < 0
}

func compareSemver(a, b string) int {
	pa, pb := semverParts(a), semverParts(b)
	for i := range 3 {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

var semverRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

func semverParts(v string) [3]int {
	var out [3]int
	m := semverRE.FindStringSubmatch(v)
	if m == nil {
		return out
	}
	for i := range 3 {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
