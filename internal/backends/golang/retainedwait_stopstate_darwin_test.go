package golang

import (
	"os"

	"golang.org/x/sys/unix"
)

func retainedStoppedWithoutConsuming(pid int) (bool, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return false, err
	}
	if info.Proc.P_pid != int32(pid) || info.Eproc.Ppid != int32(os.Getpid()) {
		return false, unix.ECHILD
	}
	// XNU bsd/sys/proc.h: SSTOP=4. Querying it does not collect wait events.
	return info.Proc.P_stat == 4, nil
}
