package host

import (
	"os"
	"runtime"
	"testing"
)

// shortDir is a temporary directory short enough to hold a Unix socket.
// t.TempDir is too deep on macOS, whose socket paths stop at 104 bytes.
func shortDir(t *testing.T) string {
	t.Helper()
	parent := os.TempDir()
	if runtime.GOOS == "darwin" {
		parent = "/tmp"
	}
	d, err := os.MkdirTemp(parent, "st")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}
