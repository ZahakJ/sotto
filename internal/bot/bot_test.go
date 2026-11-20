package bot

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ZahakJ/concord-cli/internal/fakebot"
	"github.com/ZahakJ/concord-cli/internal/proto"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakebot.Env) == "1" {
		os.Exit(fakebot.Main(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fake returns options that run this test binary as a fake concord.
func fake(t *testing.T, mode string) Options {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	return Options{
		Binary:     exe,
		Home:       home,
		Passphrase: []byte("correct horse"),
		// The parent's own CONCORD_* values must be replaced, not inherited.
		Env: []string{
			fakebot.Env + "=1", "FAKE_MODE=" + mode,
			"FAKE_WANT_PASS=correct horse", "FAKE_WANT_HOME=" + home,
			"CONCORD_PASSPHRASE=wrong", "CONCORD_HOME=/nonexistent/gui-profile",
			"PATH=" + os.Getenv("PATH"),
			"GORACE=atexit_sleep_ms=0", // the race runtime otherwise lingers 1s on exit
		},
		ReadyTimeout: 5 * time.Second,
	}
}

func TestCheckAcceptsCurrentRelease(t *testing.T) {
	o := fake(t, "")
	v, err := Check(context.Background(), o.Binary, o.Env)
	if err != nil || v != "v0.60.7" {
		t.Fatalf("Check = %q, %v", v, err)
	}
}

func TestCheckRejectsOldOrBotless(t *testing.T) {
	for _, mode := range []string{"old", "nobot"} {
		o := fake(t, mode)
		_, err := Check(context.Background(), o.Binary, o.Env)
		if !errors.Is(err, ErrTooOld) {
			t.Fatalf("%s: err = %v, want ErrTooOld", mode, err)
		}
	}
}

func TestCheckMissingBinary(t *testing.T) {
	_, err := Check(context.Background(), "/nonexistent/concord", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := Locate("", []string{"PATH=/nonexistent"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Locate err = %v", err)
	}
}

func TestStartHandshakeAndRoundTrip(t *testing.T) {
	o := fake(t, "")
	pass := o.Passphrase
	c, err := Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !slices.Equal(pass, make([]byte, len(pass))) {
		t.Fatalf("passphrase not wiped after Start: %q", pass)
	}
	if c.Ready.Fingerprint != fakebot.SelfFP || len(c.Ready.Guilds) != 2 {
		t.Fatalf("ready = %+v", c.Ready)
	}
	id, err := c.Send(proto.Command{Cmd: "send", Channel: "c-gen", Text: "hello\nworld"})
	if err != nil {
		t.Fatal(err)
	}
	e := await(t, c, "sent")
	if e.ReqID() != id || e.MessageID == "" {
		t.Fatalf("sent reply = %+v, want id %d", e, id)
	}
}

func TestWrongPassphraseIsReported(t *testing.T) {
	o := fake(t, "")
	o.Passphrase = []byte("nope")
	_, err := Start(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "wrong passphrase") {
		t.Fatalf("err = %v", err)
	}
}

func TestHandshakeFailures(t *testing.T) {
	for _, mode := range []string{"future", "garbage", "silent"} {
		o := fake(t, mode)
		o.ReadyTimeout = 1500 * time.Millisecond
		start := time.Now()
		c, err := Start(context.Background(), o)
		if err == nil {
			c.Close()
			t.Fatalf("%s: Start succeeded", mode)
		}
		if time.Since(start) > 4*time.Second {
			t.Fatalf("%s: Start took %s", mode, time.Since(start))
		}
	}
}

func TestHostileReadyIsCleaned(t *testing.T) {
	c, err := Start(context.Background(), fake(t, "hostile"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, g := range c.Ready.Guilds {
		if strings.ContainsRune(g.Name, 0x1b) {
			t.Fatalf("raw ESC in guild name %q", g.Name)
		}
		for _, ch := range g.Channels {
			if strings.ContainsRune(ch.Name, 0x1b) || strings.ContainsRune(ch.Ref, 0x1b) {
				t.Fatalf("raw ESC in channel %+v", ch)
			}
		}
	}
}

func TestCloseEndsTheChild(t *testing.T) {
	c, err := Start(context.Background(), fake(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung")
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("child still running after Close")
	}
	if _, err := c.Send(proto.Command{Cmd: "list"}); err == nil {
		t.Fatal("Send after Close succeeded")
	}
}

func TestChildEnv(t *testing.T) {
	env := childEnv([]string{"A=1", "CONCORD_PASSPHRASE=old", "CONCORD_HOME=/gui", "B=2"}, "/cli", []byte("s3cret"))
	want := []string{"A=1", "B=2", "CONCORD_HOME=/cli", "CONCORD_PASSPHRASE=s3cret"}
	if !slices.Equal(env, want) {
		t.Fatalf("childEnv = %q", env)
	}
}

func await(t *testing.T, c *Client, typ string) proto.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-c.Events():
			if !ok {
				t.Fatalf("events closed waiting for %q", typ)
			}
			if e.Type == typ {
				return e
			}
		case <-timeout:
			t.Fatalf("no %q event", typ)
		}
	}
}
