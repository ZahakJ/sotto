package host

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// checkPeer refuses a connection from any uid but uid. The socket's
// directory is already private; this holds even if its mode is loosened.
func checkPeer(conn net.Conn, uid int) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	if int(cred.Uid) != uid {
		return fmt.Errorf("connection from uid %d refused", cred.Uid)
	}
	return nil
}

// harden keeps the daemon's memory out of core dumps and out of reach of
// ptrace and /proc/<pid>/mem by other processes of the same user. concord,
// once exec'd, is dumpable again; the zero core limit is inherited.
func harden() {
	_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
	_ = unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{})
}
