//go:build !linux && !darwin

package host

import (
	"net"
	"syscall"
)

// checkPeer relies on the socket directory's mode (0700) on this system.
func checkPeer(conn net.Conn, uid int) error { return nil }

// harden disables core dumps.
func harden() {
	_ = syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{})
}
