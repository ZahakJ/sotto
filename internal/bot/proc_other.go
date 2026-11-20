//go:build !linux

package bot

import "syscall"

func sysProcAttr() *syscall.SysProcAttr { return nil }
