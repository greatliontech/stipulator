package golang

import (
	"encoding/binary"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// OpenBSD's kern.proc query includes zombprocess as well as allprocess and
// accepts a caller-sized prefix of the fixed-width kinfo_proc ABI. In
// sys/sysctl.h PID, PPID and p_stat are at offsets 108,112,304. kern_exit.c
// fixes the exit status before removing the exiting threads from ps_threads.
// fill_kproc reports SDEAD (6) once PS_EXITING is set and no active thread
// remains; kernel teardown can still be finishing, but user execution and the
// status are final. The process remains listed and retains its PID until wait
// reaps it. Unlike EVFILT_PROC, this also covers exit before registration.
func retainedBSDWait(pid int) error {
	for {
		data, err := unix.SysctlRaw("kern.proc", 1, pid, 305, 1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if len(data) != 305 || int32(binary.NativeEndian.Uint32(data[108:112])) != int32(pid) || int32(binary.NativeEndian.Uint32(data[112:116])) != int32(os.Getpid()) {
			return unix.ECHILD
		}
		if data[304] == 6 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}
