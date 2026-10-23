package golang

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func retainedStoppedWithoutConsuming(pid int) (bool, error) {
	var info retainedSolarisSiginfo
	_, _, errno := retainedSysvicall6(uintptr(unsafe.Pointer(&retainedLibcWaitid)), 4, 0, uintptr(pid), uintptr(unsafe.Pointer(&info)), unix.WSTOPPED|unix.WNOWAIT|unix.WNOHANG, 0, 0)
	runtime.KeepAlive(&info)
	if errno != 0 {
		return false, errno
	}
	// P_PID=0; sys/siginfo.h: CLD_STOPPED=5. No event leaves si_pid zero.
	return info.PID == int32(pid) && info.Code == 5, nil
}
