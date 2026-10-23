//go:build freebsd || netbsd || dragonfly

package golang

import "syscall"

// As in os.blockUntilWaitable, wait6(WEXITED|WNOWAIT) waits for an exit,
// including an already-exited child, while preserving it for the final reap.
func retainedBSDWait(pid int) error {
	for {
		err := retainedWait6(pid)
		if err == syscall.EINTR {
			continue
		}
		if err != 0 {
			return err
		}
		return nil
	}
}
