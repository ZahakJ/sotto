package proto

import (
	"strings"
	"testing"
)

func TestDecodeMessageIsCleaned(t *testing.T) {
	line := `{"type":"message","guildId":"g1","guild":"Ops\u001b[2J","channelId":"c1","channel":"gen\u001b]0;x\u0007","messageId":"m1","sender":"AAAA BBBB","senderName":"\u001b[31mroot` + "\xe2\x80\xae" /* U+202E */ + `","content":"hi\u001b]52;c;Zm9v\u0007\nthere\r","sent":"2026-11-19T10:00:00Z"}`
	e, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{e.GuildName, e.Channel, e.SenderName, e.Content} {
		if strings.ContainsAny(s, "\x1b\x07\r\xe2\x80\xae") {
			t.Fatalf("control survived decode: %q", s)
		}
	}
	if e.Content != "hi^[]52;c;Zm9v^G\nthere^M" {
		t.Fatalf("content = %q", e.Content)
	}
	if e.GuildName != "Ops^[[2J" {
		t.Fatalf("guild = %q", e.GuildName)
	}
}

func TestDecodeJoinedGuildObject(t *testing.T) {
	e, err := Decode([]byte(`{"id":7,"type":"joined","guild":{"id":"g","name":"Den\u009b","channels":[{"id":"c","name":"general","ref":"Den/#general"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.Joined == nil || e.Joined.Name != "Den\xef\xbf\xbd" /* U+FFFD */ || len(e.Joined.Channels) != 1 || e.ReqID() != 7 {
		t.Fatalf("joined = %+v id=%d", e.Joined, e.ReqID())
	}
}

func TestDecodeIgnoresForeignIDs(t *testing.T) {
	e, err := Decode([]byte(`{"id":"\u001b[2J","type":"error","error":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != nil || e.ReqID() != 0 {
		t.Fatalf("non-numeric id kept: %q", e.ID)
	}
}

func TestDecodeRejectsJunk(t *testing.T) {
	for _, l := range []string{"", "not json", `{"no":"type"}`, `{"type":"joined","guild":5}`} {
		if _, err := Decode([]byte(l)); err == nil {
			t.Fatalf("Decode(%q) accepted", l)
		}
	}
}

func TestEncodeEventRoundTrips(t *testing.T) {
	for _, line := range []string{
		`{"id":4,"type":"joined","guild":{"id":"g","name":"Den","channels":[{"id":"c","name":"general","ref":"Den/#general"}]}}`,
		`{"type":"member","action":"join","guildId":"g","guild":"Den","fingerprint":"F","name":"alice"}`,
		`{"type":"message","guildId":"g","channelId":"c","messageId":"m","sender":"F","senderName":"a","content":"two\nlines","sent":"2026-11-19T10:00:00Z"}`,
	} {
		e, err := Decode([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		b, err := EncodeEvent(e)
		if err != nil {
			t.Fatal(err)
		}
		e2, err := Decode(b)
		if err != nil {
			t.Fatal(err)
		}
		b2, _ := EncodeEvent(e2)
		if string(b) != string(b2) || e2.GuildName != e.GuildName || (e.Joined != nil) != (e2.Joined != nil) || e2.ReqID() != e.ReqID() {
			t.Fatalf("round trip changed the event:\n%s\n%s", b, b2)
		}
	}
}

func TestEncodeIsOneLine(t *testing.T) {
	b, err := Encode(Command{ID: 3, Cmd: "send", Channel: "c", Text: "a\nb\r\n{\"cmd\":\"quit\"}"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "\n") != 1 || !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("encoded command spans lines: %q", b)
	}
}

func TestCheckReady(t *testing.T) {
	ok := Event{Type: "ready", Fingerprint: "F", PeerID: "P"}
	if err := CheckReady(ok); err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{
		{Type: "message"},
		{Type: "ready", PeerID: "P"},
		{Type: "ready", Fingerprint: "F", PeerID: "P", Protocol: Version + 1},
	} {
		if err := CheckReady(e); err == nil {
			t.Fatalf("CheckReady(%+v) passed", e)
		}
	}
}

func TestVersion(t *testing.T) {
	cases := []struct {
		out  string
		want string
		old  bool
	}{
		{"concord v0.60.7 (headless, linux/amd64)\n", "v0.60.7", false},
		{"concord v0.59.0 (headless, linux/amd64)", "v0.59.0", false},
		{"concord v0.58.9 (headless, linux/amd64)", "v0.58.9", true},
		{"concord v1.0.0-rc1 (headless, linux/amd64)", "v1.0.0-rc1", false},
		{"concord dev (headless, linux/amd64)", "dev", false},
	}
	for _, c := range cases {
		v, err := ParseVersion(c.out)
		if err != nil || v != c.want || TooOld(v) != c.old {
			t.Fatalf("%q: v=%q err=%v old=%v", c.out, v, err, TooOld(v))
		}
	}
	if _, err := ParseVersion("\x1b[2Jsomething else"); err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("bad version output: err=%v", err)
	}
}
