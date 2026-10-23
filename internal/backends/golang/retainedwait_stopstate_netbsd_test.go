package golang

import (
	"syscall"
	"unsafe"
)

func retainedStoppedWithoutConsuming(pid int) (bool, error) {
	var status int32
	// NetBSD wait6 uses P_PID=1 on every Go architecture.
	// WUNTRACED is the stop-request flag; syscall.WSTOPPED is the historical
	// wait-status mask, not the flag in current sys/sys/wait.h.
	observedPID, _, errno := syscall.Syscall6(syscall.SYS_WAIT6, 1, uintptr(pid), uintptr(unsafe.Pointer(&status)), syscall.WUNTRACED|syscall.WNOWAIT|syscall.WNOHANG, 0, 0)
	if errno != 0 {
		return false, errno
	}
	// WNOHANG returning zero does not establish a valid status record.
	return observedPID == uintptr(pid) && syscall.WaitStatus(status).Stopped() && syscall.WaitStatus(status).StopSignal() == syscall.SIGSTOP, nil
}
