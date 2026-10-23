package golang

import (
	"syscall"
	"unsafe"
)

func retainedWait6(pid int) syscall.Errno {
	var status int32
	_, _, err := syscall.Syscall6(syscall.SYS_WAIT6, 1, uintptr(pid), uintptr(unsafe.Pointer(&status)), syscall.WEXITED|syscall.WNOWAIT, 0, 0)
	return err
}
