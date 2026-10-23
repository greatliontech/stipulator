package golang

import (
	"syscall"
	"unsafe"
)

func retainedWait6(pid int) syscall.Errno {
	var status int32
	// FreeBSD's 32-bit ABI splits id_t into two words.
	_, _, err := syscall.Syscall9(syscall.SYS_WAIT6, 0, uintptr(pid), 0, uintptr(unsafe.Pointer(&status)), syscall.WEXITED|syscall.WNOWAIT, 0, 0, 0, 0)
	return err
}
