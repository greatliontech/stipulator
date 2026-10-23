//go:build dragonfly || (freebsd && (amd64 || arm64 || riscv64))

package golang

import (
	"syscall"
	"unsafe"
)

func retainedWait6(pid int) syscall.Errno {
	var status int32
	// P_PID is zero on FreeBSD and DragonFly, unlike NetBSD.
	_, _, err := syscall.Syscall6(syscall.SYS_WAIT6, 0, uintptr(pid), uintptr(unsafe.Pointer(&status)), syscall.WEXITED|syscall.WNOWAIT, 0, 0)
	return err
}
