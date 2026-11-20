package host

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// checkPeer refuses a connection from any uid but uid.
func checkPeer(conn net.Conn, uid int) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Xucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
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

// harden disables core dumps; macOS has no PR_SET_DUMPABLE.
func harden() {
	_ = unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{})
}
