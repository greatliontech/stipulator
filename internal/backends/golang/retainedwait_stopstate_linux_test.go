package golang

import "golang.org/x/sys/unix"

func retainedStoppedWithoutConsuming(pid int) (bool, error) {
	var info unix.Siginfo
	err := unix.Waitid(unix.P_PID, pid, &info, unix.WSTOPPED|unix.WNOWAIT|unix.WNOHANG, nil)
	// Linux siginfo.h: CLD_STOPPED=5. WNOHANG leaves Code zero until the stop.
	return err == nil && info.Code == 5, err
}
