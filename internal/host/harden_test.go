package host

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ZahakJ/concord-cli/internal/bot"
	"github.com/ZahakJ/concord-cli/internal/proto"
)

// The socket is checked against the peer's uid, not only by the
// directory's permissions.
func TestPeerUIDIsChecked(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := net.Dial("unix", sock)
		if err == nil {
			time.Sleep(time.Second)
			c.Close()
		}
	}()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := checkPeer(conn, os.Getuid()); err != nil {
		t.Fatalf("own uid refused: %v", err)
	}
	if err := checkPeer(conn, os.Getuid()+1); err == nil {
		t.Fatal("a different uid was accepted")
	}
}

// The daemon holds the passphrase's HMAC key and talks to concord; it must
// not be readable through /proc by the same user nor leave a core dump.
func TestDaemonIsNotDumpable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	base := t.TempDir()
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "--", "--home "+base).Run() })
	c, err := bot.Start(context.Background(), attach(t, base, "pw"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = c.Send(proto.Command{Cmd: "host-stop"})
		c.Close()
	}()
	out, _ := exec.Command("pgrep", "-f", "--", "--daemon --home "+base).Output()
	pids := strings.Fields(string(out))
	if len(pids) != 1 {
		t.Fatalf("daemon pids %q", pids)
	}
	if _, err := os.ReadFile("/proc/" + pids[0] + "/environ"); err == nil {
		t.Error("the daemon's /proc/<pid>/environ is readable (still dumpable)")
	}
	limits, err := os.ReadFile("/proc/" + pids[0] + "/limits")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(limits), "\n") {
		if strings.HasPrefix(l, "Max core file size") {
			if f := strings.Fields(l); len(f) < 6 || f[4] != "0" || f[5] != "0" {
				t.Errorf("core limit: %q", l)
			}
		}
	}
}
