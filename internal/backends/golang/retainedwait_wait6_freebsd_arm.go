package golang

import (
	"syscall"
	"unsafe"
)

func retainedWait6(pid int) syscall.Errno {
	var status int32
	// ARM additionally aligns the two-word id_t to an even word boundary.
	_, _, err := syscall.Syscall9(syscall.SYS_WAIT6, 0, 0, uintptr(pid), 0, uintptr(unsafe.Pointer(&status)), syscall.WEXITED|syscall.WNOWAIT, 0, 0, 0)
	return err
}
