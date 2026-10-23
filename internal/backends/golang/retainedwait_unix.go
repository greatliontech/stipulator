//go:build solaris

package golang

import (
	"errors"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const retainedCanObserveExit = true

func retainedAwaitExit(p *retainedCommand) error {
	pid := p.cmd.Process.Pid
	for {
		var info retainedSolarisSiginfo
		// sys/procset.h defines P_PID as zero. Solaris and illumos Go ports
		// are amd64: libc receives the LP64 siginfo_t defined below.
		_, _, errno := retainedSysvicall6(uintptr(unsafe.Pointer(&retainedLibcWaitid)), 4, 0, uintptr(pid), uintptr(unsafe.Pointer(&info)), unix.WEXITED|unix.WNOWAIT, 0, 0)
		runtime.KeepAlive(&info)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		// sys/siginfo.h: CLD_EXITED=1, CLD_KILLED=2, CLD_DUMPED=3.
		if info.PID != int32(pid) || info.Code < 1 || info.Code > 3 {
			return unix.ECHILD
		}
		return nil
	}
}

// Solaris/illumos expose waitid through libc, but x/sys/unix does not wrap it.
// Use the same supported syscall.sysvicall6 bridge as x/sys/unix. wait4's
// option mask does not admit WNOWAIT; only waitid preserves the child here.
// LP64 sys/siginfo.h defines a 256-byte, 8-byte-aligned record, si_code at 4
// and si_pid at 16. The zero-length member preserves the union's alignment.
type retainedSolarisSiginfo struct {
	_                       [0]uint64
	Signo, Code, Errno, Pad int32
	PID                     int32
	_                       [236]byte
}

//go:cgo_import_dynamic libc_retained_waitid waitid "libc.so"
//go:linkname retainedLibcWaitid libc_retained_waitid
var retainedLibcWaitid uintptr

//go:linkname retainedSysvicall6 syscall.sysvicall6
//go:uintptrescapes
func retainedSysvicall6(trap, nargs, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

func retainedSweep(cmd *exec.Cmd, _ func() error) error {
	pid := cmd.Process.Pid
	err := unix.Kill(-pid, unix.SIGKILL)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}
