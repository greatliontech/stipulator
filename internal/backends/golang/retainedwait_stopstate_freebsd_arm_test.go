package golang

import (
	"syscall"
	"unsafe"
)

func retainedStoppedWithoutConsuming(pid int) (bool, error) {
	var status int32
	// FreeBSD/ARM wait6 aligns the two-word id_t after P_PID=0.
	observedPID, _, errno := syscall.Syscall9(syscall.SYS_WAIT6, 0, 0, uintptr(pid), 0, uintptr(unsafe.Pointer(&status)), syscall.WUNTRACED|syscall.WNOWAIT|syscall.WNOHANG, 0, 0, 0)
	if errno != 0 {
		return false, errno
	}
	// WNOHANG returning zero does not establish a valid status record.
	return observedPID == uintptr(pid) && syscall.WaitStatus(status).Stopped() && syscall.WaitStatus(status).StopSignal() == syscall.SIGSTOP, nil
}
