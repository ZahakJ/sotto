// Package fakebot is a stand-in for `concord --bot`, for tests. A test binary
// re-executes itself with SOTTO_FAKE=1 and calls Main from TestMain, so
// the client is exercised against a real child process: pipes, environment,
// exit codes and all, without a network.
//
// FAKE_MODE picks a behaviour:
//
//	""        a well-behaved protocol v1 bot
//	old       --version reports a release before --bot existed
//	nobot     --help does not mention --bot
//	future    ready announces protocol 2
//	garbage   the first line is not JSON
//	silent    never says ready
//	hostile   every remote string in ready carries escape sequences
//
// FAKE_WANT_PASS is the passphrase the fake insists on, and FAKE_WANT_HOME the
// CONCORD_HOME; a mismatch exits 1 the way concord does on a wrong passphrase.
package fakebot

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ZahakJ/sotto/internal/proto"
)

// Env is the variable that turns a test binary into the fake.
const Env = "SOTTO_FAKE"

// Escape sequences a hostile peer would try. Shared with the tests that
// check none of them reach the screen.
const (
	EvilName    = "\x1b[31mmallory\x1b]0;owned\x07"
	EvilContent = "hi\x1b[2J\x1b[H\x1b]52;c;cm0gLXJmIH4=\x07\x9b6n\r\x1b]8;;https://x/\x1b\\link"
)

// Main runs the fake and returns its exit code.
func Main(args []string) int {
	mode := os.Getenv("FAKE_MODE")
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	switch arg {
	case "--version":
		switch mode {
		case "old":
			fmt.Println("concord v0.58.3 (headless, linux/amd64)")
		default:
			fmt.Println("concord v0.60.7 (headless, linux/amd64)")
		}
		return 0
	case "--help":
		if mode == "nobot" {
			fmt.Println("Usage: concord [--status | --serve | --version | --help]")
		} else {
			fmt.Println("Usage: concord [--status | --serve | --bot | --version | --help]")
		}
		return 0
	case "--bot":
	case "host-wait":
		// Plays an attach host whose daemon is backing off: greeting,
		// handshake, two WAIT lines over 1.5s, then the bot protocol.
		fmt.Println("HOST " + proto.HelloVersion + " existing")
		var line []byte
		for b := make([]byte, 1); len(line) < 4096; {
			if n, _ := os.Stdin.Read(b); n == 0 {
				break
			}
			if line = append(line, b[0]); b[0] == '\n' {
				break
			}
		}
		if _, err := proto.ReadHello(bufio.NewReader(bytes.NewReader(line))); err != nil {
			fmt.Fprintln(os.Stderr, "fake host:", err)
			return 1
		}
		fmt.Println("WAIT 2")
		time.Sleep(750 * time.Millisecond)
		fmt.Println("WAIT 1")
		time.Sleep(750 * time.Millisecond)
	default:
		fmt.Fprintln(os.Stderr, "fake: unexpected arguments", args)
		return 2
	}

	if want := os.Getenv("FAKE_WANT_PASS"); want != "" && os.Getenv("CONCORD_PASSPHRASE") != want {
		fmt.Fprintln(os.Stderr, "concord: wrong passphrase")
		return 1
	}
	if want := os.Getenv("FAKE_WANT_HOME"); want != "" && os.Getenv("CONCORD_HOME") != want {
		fmt.Fprintln(os.Stderr, "concord: wrong CONCORD_HOME", os.Getenv("CONCORD_HOME"))
		return 1
	}
	for _, kv := range os.Environ() {
		if strings.Count(kv, "CONCORD_PASSPHRASE=") > 0 && !strings.HasPrefix(kv, "CONCORD_PASSPHRASE=") {
			fmt.Fprintln(os.Stderr, "fake: passphrase leaked into", strings.SplitN(kv, "=", 2)[0])
			return 1
		}
	}
	fmt.Fprintln(os.Stderr, "concord bot FAKE running")

	out := json.NewEncoder(os.Stdout)
	out.SetEscapeHTML(false)
	var mu sync.Mutex
	emit := func(v map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		_ = out.Encode(v)
	}

	switch mode {
	case "garbage":
		fmt.Println("hello, I am not JSON")
	case "silent":
		time.Sleep(time.Hour)
		return 0
	}

	ready := map[string]any{
		"type": "ready", "fingerprint": SelfFP, "peerId": "12D3KooWfake", "name": "me",
		"guilds": guilds(mode == "hostile"),
	}
	if mode == "future" {
		ready["protocol"] = 2
	}
	emit(ready)

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 64*1024), 1<<20)
	for in.Scan() {
		var c map[string]any
		if err := json.Unmarshal(in.Bytes(), &c); err != nil {
			emit(map[string]any{"type": "error", "error": "bad command line: " + err.Error()})
			continue
		}
		id := c["id"]
		reply := func(v map[string]any) {
			if id != nil {
				v["id"] = id
			}
			emit(v)
		}
		str := func(k string) string { s, _ := c[k].(string); return s }
		switch str("cmd") {
		case "list":
			reply(map[string]any{"type": "guilds", "guilds": guilds(mode == "hostile")})
		case "members":
			reply(map[string]any{"type": "members", "guildId": "g-den", "members": []map[string]any{
				{"fingerprint": SelfFP, "name": "me"},
				{"fingerprint": AliceFP, "name": "alice"},
				{"fingerprint": MalloryFP, "name": EvilName},
			}})
		case "send":
			ch := str("channel")
			reply(map[string]any{"type": "sent", "channelId": ch, "messageId": fmt.Sprintf("m-%v", id)})
			// A peer answers, so the tests see an incoming message.
			if str("text") == "!later" { // ... a little later, after a detach
				go func() {
					time.Sleep(400 * time.Millisecond)
					emit(map[string]any{
						"type": "message", "guildId": "g-den", "guild": "Den", "channelId": ch, "channel": "general",
						"messageId": "later-1", "sender": AliceFP, "senderName": "alice",
						"content": "sent while you were away", "sent": time.Now().UTC().Format(time.RFC3339Nano),
					})
				}()
			}
			if str("text") == "!hostile" {
				emit(map[string]any{
					"type": "message", "guildId": "g-den", "guild": "Den", "channelId": ch, "channel": "general",
					"messageId": "evil-1", "sender": MalloryFP, "senderName": EvilName,
					"content": EvilContent, "sent": time.Now().UTC().Format(time.RFC3339Nano),
				})
			}
		case "join":
			if str("invite") != "GOODCODE" {
				reply(map[string]any{"type": "error", "error": "invite: not a valid invite code"})
				continue
			}
			g := map[string]any{"id": "g-new", "name": "Workshop", "channels": []map[string]any{
				{"id": "c-ws", "name": "general", "ref": "Workshop/#general"}}}
			reply(map[string]any{"type": "joined", "guild": g})
			emit(map[string]any{"type": "guild", "action": "join", "guildId": "g-new", "guild": "Workshop"})
		case "name":
			reply(map[string]any{"type": "ok", "cmd": "name"})
		case "quit":
			reply(map[string]any{"type": "ok", "cmd": "quit"})
			return 0
		default:
			reply(map[string]any{"type": "error", "error": fmt.Sprintf("unknown cmd %q", str("cmd"))})
		}
	}
	return 0
}

// Fingerprints the fake uses, in Concord's grouped base32 form.
const (
	SelfFP    = "SELF SELF SELF SELF SELF SELF SELF SELF"
	AliceFP   = "ALIC EALI CEAL ICEA LICE ALIC EALI CEAL"
	MalloryFP = "MALL ORYM ALLO RYMA LLOR YMAL LORY MALL"
)

func guilds(hostile bool) []map[string]any {
	den, gen := "Den", "general"
	if hostile {
		den, gen = "Den\x1b]0;owned\x07", "gen\x1b[2Jeral"
	}
	return []map[string]any{
		{"id": "g-den", "name": den, "channels": []map[string]any{
			{"id": "c-gen", "name": gen, "ref": den + "/#" + gen},
			{"id": "c-rand", "name": "random", "ref": den + "/#random"},
			{"id": "c-voice", "name": "lounge", "type": "voice", "ref": den + "/#lounge"},
		}},
		{"id": "g-dm", "name": "alice", "dm": true, "channels": []map[string]any{
			{"id": "c-dm", "name": "dm", "ref": "alice/#dm"},
		}},
	}
}
