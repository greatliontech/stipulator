//go:build dragonfly || (freebsd && (amd64 || arm64 || riscv64))

package golang

import (
	"syscall"
	"unsafe"
)

func retainedStoppedWithoutConsuming(pid int) (bool, error) {
	var status int32
	// wait6's P_PID is zero, and id_t occupies one syscall argument here.
	// WUNTRACED is the stop-request flag; syscall.WSTOPPED on DragonFly is
	// the historical wait-status mask, not the flag in current sys/wait.h.
	observedPID, _, errno := syscall.Syscall6(syscall.SYS_WAIT6, 0, uintptr(pid), uintptr(unsafe.Pointer(&status)), syscall.WUNTRACED|syscall.WNOWAIT|syscall.WNOHANG, 0, 0)
	if errno != 0 {
		return false, errno
	}
	// WNOHANG returning zero does not establish a valid status record.
	return observedPID == uintptr(pid) && syscall.WaitStatus(status).Stopped() && syscall.WaitStatus(status).StopSignal() == syscall.SIGSTOP, nil
}
