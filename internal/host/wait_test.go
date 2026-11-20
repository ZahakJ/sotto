package host

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZahakJ/sotto/internal/bot"
	"github.com/ZahakJ/sotto/internal/proto"
)

// The daemon may hold a handshake for minutes while it backs off. Attach
// used to give up after a fixed deadline and report that the daemon had
// closed the connection; it now relays WAIT lines and extends its deadline.
func TestAttachRelaysWaitAndOutlastsTheDeadline(t *testing.T) {
	old := statusTimeout
	statusTimeout = 500 * time.Millisecond
	t.Cleanup(func() { statusTimeout = old })

	base := shortDir(t)
	cfg := Config{Home: filepath.Join(base, "identity"), Sock: filepath.Join(base, "host.sock")}
	ln, err := net.Listen("unix", cfg.Sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // a daemon that makes the client wait 1.2s
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if _, err := proto.ReadHello(bufio.NewReader(c)); err != nil {
				c.Close() // Attach's probe for a running daemon
				continue
			}
			c.Write([]byte("WAIT 1\n"))
			time.Sleep(600 * time.Millisecond)
			c.Write([]byte("WAIT 1\n"))
			time.Sleep(600 * time.Millisecond)
			c.Write([]byte("OK\n{\"type\":\"ready\"}\n"))
			c.Close()
			return
		}
	}()

	// stdin stays open, as a transport's does, so that Attach returns
	// when the daemon closes.
	in, inW := io.Pipe()
	defer inW.Close()
	go func() { _ = proto.WriteHello(inW, []byte("pw")) }()
	var out bytes.Buffer
	if err := Attach(cfg, in, &out); err != nil {
		t.Fatalf("attach: %v (output %q)", err, out.String())
	}
	got := out.String()
	want := "HOST " + proto.HelloVersion + " existing\nWAIT 1\nWAIT 1\n{\"type\":\"ready\"}\n"
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// After wrong guesses the real daemon tells a waiting client how long, and
// the right passphrase still gets in.
func TestBackoffIsAnnouncedToTheClient(t *testing.T) {
	base := shortDir(t)
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "--", "--home "+base).Run() })
	c, err := bot.Start(context.Background(), attach(t, base, "pw"))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	time.Sleep(1100 * time.Millisecond)
	for range 3 { // backoff 1s, 2s, then 4s before the next check
		if _, err := bot.Start(context.Background(), attach(t, base, "typo")); err == nil {
			t.Fatal("wrong passphrase accepted")
		}
	}
	var waits []int
	o := attach(t, base, "pw")
	o.ReadyTimeout = 3 * time.Second // shorter than the backoff
	o.Wait = func(s int) { waits = append(waits, s) }
	c, err = bot.Start(context.Background(), o)
	if err != nil {
		t.Fatalf("after backoff: %v", err)
	}
	if len(waits) == 0 || waits[0] < 2 {
		t.Errorf("waits announced: %v", waits)
	}
	_, _ = c.Send(proto.Command{Cmd: "host-stop"})
	c.Close()
}
