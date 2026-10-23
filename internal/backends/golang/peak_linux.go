//go:build linux

package golang

import (
	"os"
	"syscall"
)

// processPeakBytes reads a reaped package process's largest resident set
// from its wait status: the kernel's maxrss over the process and the
// descendants it waited for — the go driver and the test binary beneath
// it — in kibibytes, so the value is the largest single process of the
// package's tree, the admission's completed-package evidence. 0 when the
// state carries no usage.
func processPeakBytes(state *os.ProcessState) uint64 {
	if state == nil {
		return 0
	}
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok || usage.Maxrss <= 0 {
		return 0
	}
	return uint64(usage.Maxrss) * 1024
}

// parentPeakBytes reads this process's own lifetime high-water resident set
// (RUSAGE_SELF.Maxrss) in bytes. On Linux, children spawned with
// CLONE_VM | CLONE_VFORK inherit the parent's mm_struct high-water mark into
// signal->maxrss at exec_mmap, clamping the child's wait-status Maxrss from
// below by this value.
func parentPeakBytes() uint64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil || usage.Maxrss <= 0 {
		return 0
	}
	return uint64(usage.Maxrss) * 1024
}
