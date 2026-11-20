package host

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ZahakJ/concord-cli/internal/bot"
	"github.com/ZahakJ/concord-cli/internal/proto"
)

// waitFor reports whether an event with the given message id arrives in d.
func waitFor(c *bot.Client, id string, d time.Duration) bool {
	timeout := time.After(d)
	for {
		select {
		case e, ok := <-c.Events():
			if !ok {
				return false
			}
			if e.MessageID == id {
				return true
			}
		case <-timeout:
			return false
		}
	}
}

// Ported from the review's TestPoCHost (b): a connection that authenticates
// and dies at once used to take the replay buffer with it.
func TestAbortedAttachKeepsTheReplay(t *testing.T) {
	base := t.TempDir()
	sock := filepath.Join(base, "host.sock")
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "--", "--home "+base).Run() })
	c1, err := bot.Start(context.Background(), attach(t, base, "pw"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c1.Send(proto.Command{Cmd: "send", Channel: "c-gen", Text: "!later"})
	next(t, c1, "sent")
	c1.Close()
	time.Sleep(900 * time.Millisecond) // later-1 arrives while detached

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	_ = proto.WriteHello(c, []byte("pw"))
	buf := make([]byte, 3)
	_, _ = c.Read(buf) // "OK\n": authenticated
	c.Close()
	time.Sleep(1200 * time.Millisecond) // past the limiter's gap

	c2, err := bot.Start(context.Background(), attach(t, base, "pw"))
	if err != nil {
		t.Fatal(err)
	}
	if !waitFor(c2, "later-1", 3*time.Second) {
		t.Fatal("the missed message was lost to an attach that never acknowledged it")
	}
	r := next(t, c2, "replayed")
	if r.Count != 1 || r.Dropped != 0 {
		t.Fatalf("replayed marker %+v", r)
	}
	c2.Close() // acknowledged, so the next attach gets no repeat
	time.Sleep(1200 * time.Millisecond)

	c3, err := bot.Start(context.Background(), attach(t, base, "pw"))
	if err != nil {
		t.Fatal(err)
	}
	if waitFor(c3, "later-1", time.Second) {
		t.Fatal("an acknowledged replay was sent again")
	}
	_, _ = c3.Send(proto.Command{Cmd: "host-stop"})
	c3.Close()
}

func TestMissedOverflowIsCounted(t *testing.T) {
	d := &daemon{}
	for i := range missedCap + 7 {
		d.store([]byte(strconv.Itoa(i) + "\n"))
	}
	if len(d.missed) != missedCap || d.dropped != 7 {
		t.Fatalf("kept %d, dropped %d", len(d.missed), d.dropped)
	}
	if string(d.missed[0]) != "7\n" {
		t.Fatalf("oldest kept %q", d.missed[0])
	}
}

// A host with no identity is announced as new before the passphrase is
// asked for, so the client can ask twice; a running host or an existing
// identity is not.
func TestFirstRemoteAttachAsksTwice(t *testing.T) {
	base := t.TempDir()
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "--", "--home "+base).Run() })
	var asked []bool
	ask := func(confirm bool) ([]byte, error) {
		asked = append(asked, confirm)
		return []byte("pw"), nil
	}
	start := func() *bot.Client {
		t.Helper()
		o := attach(t, base, "")
		o.Passphrase, o.Ask = nil, ask
		c, err := bot.Start(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := start() // nothing there yet
	c.Close()
	time.Sleep(1100 * time.Millisecond)
	c = start() // daemon running
	_, _ = c.Send(proto.Command{Cmd: "host-stop"})
	c.Close()
	time.Sleep(time.Second)
	_ = os.MkdirAll(filepath.Join(base, "identity"), 0o700)
	if err := os.WriteFile(filepath.Join(base, "identity", "key"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	c = start() // daemon stopped, identity on disk
	_, _ = c.Send(proto.Command{Cmd: "host-stop"})
	c.Close()
	if fmt.Sprint(asked) != "[true false false]" {
		t.Fatalf("confirm asked as %v, want [true false false]", asked)
	}
}
