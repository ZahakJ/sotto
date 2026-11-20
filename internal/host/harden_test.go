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

	"github.com/ZahakJ/sotto/internal/bot"
	"github.com/ZahakJ/sotto/internal/proto"
)

// The socket is checked against the peer's uid, not only by the
// directory's permissions.
func TestPeerUIDIsChecked(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s")
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
	base := shortDir(t)
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

// A data directory deep enough to push the socket path past the system's
// limit (104 bytes on macOS, 108 on Linux) is refused with a clear error.
func TestLongSocketPathIsRefused(t *testing.T) {
	home := filepath.Join(shortDir(t), strings.Repeat("d", 110))
	var out, errb strings.Builder
	if code := Main([]string{"--home", home}, strings.NewReader(""), &out, &errb); code == 0 {
		t.Fatal("accepted")
	}
	if !strings.Contains(errb.String(), "too long") || !strings.Contains(errb.String(), "--home") {
		t.Fatalf("stderr %q", errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("greeted anyway: %q", out.String())
	}
}
