package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZahakJ/sotto/internal/bot"
	"github.com/ZahakJ/sotto/internal/fakebot"
	"github.com/ZahakJ/sotto/internal/proto"
)

// The test binary plays three parts: the host (`<exe> host …`), the
// detached daemon it starts, and, through fakebot, concord itself.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "host" {
		os.Exit(Main(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if os.Getenv(fakebot.Env) == "1" {
		os.Exit(fakebot.Main(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// attach returns options that reach the host the way a mesh would: through
// a transport command. `env -i` stands in for `jaunt run box --`; it also
// wipes the environment, so the passphrase can only arrive down stdin.
func attach(t *testing.T, base, pass string) bot.Options {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return bot.Options{
		Via: []string{"env", "-i", "PATH=" + os.Getenv("PATH"), "HOME=" + base,
			fakebot.Env + "=1", "FAKE_WANT_PASS=pw", "GORACE=atexit_sleep_ms=0"},
		Remote:       []string{exe, "host", "--home", base, "--concord", exe},
		Passphrase:   []byte(pass),
		Env:          []string{"CONCORD_PASSPHRASE=must-not-travel", "PATH=" + os.Getenv("PATH")},
		ReadyTimeout: 10 * time.Second,
	}
}

func next(t *testing.T, c *bot.Client, typ string) proto.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-c.Events():
			if !ok {
				t.Fatalf("connection closed waiting for %q", typ)
			}
			if e.Type == typ {
				return e
			}
		case <-timeout:
			t.Fatalf("no %q", typ)
		}
	}
}

func TestAttachDetachReattach(t *testing.T) {
	base := t.TempDir()
	sock := filepath.Join(base, "host.sock")
	t.Cleanup(func() {
		if _, err := os.Stat(sock); err == nil { // a daemon outlived the test
			_ = exec.Command("pkill", "-f", "--", "--home "+base).Run()
		}
	})

	// First attach starts the daemon with this passphrase.
	c1, err := bot.Start(context.Background(), attach(t, base, "pw"))
	if err != nil {
		t.Fatal(err)
	}
	if c1.Ready.Fingerprint != fakebot.SelfFP {
		t.Fatalf("ready = %+v", c1.Ready)
	}
	id, err := c1.Send(proto.Command{Cmd: "send", Channel: "c-gen", Text: "!hostile"})
	if err != nil {
		t.Fatal(err)
	}
	if e := next(t, c1, "sent"); e.ReqID() != id {
		t.Fatalf("reply id %d, want %d (ids must be mapped back)", e.ReqID(), id)
	}
	if e := next(t, c1, "message"); strings.ContainsRune(e.Content, 0x1b) || strings.ContainsRune(e.SenderName, 0x1b) {
		t.Fatalf("relayed message not clean: %+v", e)
	}

	// Ask for a message that arrives after we leave, then detach.
	if _, err := c1.Send(proto.Command{Cmd: "send", Channel: "c-gen", Text: "!later"}); err != nil {
		t.Fatal(err)
	}
	next(t, c1, "sent")
	c1.Close()
	time.Sleep(800 * time.Millisecond)
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("daemon gone after detach: %v", err)
	}

	// A wrong passphrase does not attach.
	if c, err := bot.Start(context.Background(), attach(t, base, "nope")); err == nil {
		c.Close()
		t.Fatal("attached with the wrong passphrase")
	} else if !strings.Contains(err.Error(), "wrong passphrase") {
		t.Fatalf("wrong-passphrase error = %v", err)
	}

	// Another device attaches and gets what it missed.
	c2, err := bot.Start(context.Background(), attach(t, base, "pw"))
	if err != nil {
		t.Fatal(err)
	}
	if e := next(t, c2, "message"); e.MessageID != "later-1" {
		t.Fatalf("missed message not replayed: %+v", e)
	}

	// /shutdown stops the daemon and its concord.
	if _, err := c2.Send(proto.Command{Cmd: "host-stop"}); err != nil {
		t.Fatal(err)
	}
	c2.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(sock); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon still listening after host-stop")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestFirstAttachWithWrongPassphraseStartsNothing(t *testing.T) {
	base := t.TempDir()
	_, err := bot.Start(context.Background(), attach(t, base, "nope"))
	if err == nil || !strings.Contains(err.Error(), "wrong passphrase") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "host.sock")); err == nil {
		t.Fatal("a daemon is listening after a failed start")
	}
}
