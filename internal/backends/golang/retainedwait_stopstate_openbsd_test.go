package golang

import (
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
)

func retainedStoppedWithoutConsuming(pid int) (bool, error) {
	// The fixed-width sys/sysctl.h kinfo_proc prefix includes PID, PPID and
	// p_stat at 108, 112 and 304. This query does not collect wait events.
	data, err := unix.SysctlRaw("kern.proc", 1, pid, 305, 1)
	if err != nil {
		return false, err
	}
	if len(data) != 305 || int32(binary.NativeEndian.Uint32(data[108:112])) != int32(pid) || int32(binary.NativeEndian.Uint32(data[112:116])) != int32(os.Getpid()) {
		return false, unix.ECHILD
	}
	return data[304] == 4, nil // sys/proc.h: SSTOP=4.
}
