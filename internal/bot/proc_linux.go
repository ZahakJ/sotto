package bot

import "syscall"

// sysProcAttr asks the kernel to stop concord if this process dies without
// cleaning up, so a crashed client never leaves a headless peer running.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
