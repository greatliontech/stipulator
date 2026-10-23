package golang

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Darwin's EVFILT_PROC attaches only to future exits, and waitid(WEXITED)
// historically also reports stops (Go issue 19314). kern.proc.pid instead
// exposes the unreaped child, including an exit predating this observation.
// XNU bsd/sys/proc.h defines SZOMB=5 as awaiting collection by the parent;
// kern_sysctl.c's KERN_PROC_PID query includes zombies. Never infer exit from
// ESRCH: require this PID, this parent, and the terminal state together.
func retainedBSDWait(pid int) error {
	for {
		info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Proc.P_pid != int32(pid) || info.Eproc.Ppid != int32(os.Getpid()) {
			return unix.ECHILD
		}
		if info.Proc.P_stat == 5 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}
