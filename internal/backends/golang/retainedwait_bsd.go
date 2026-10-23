//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package golang

import (
	"errors"
	"os/exec"

	"golang.org/x/sys/unix"
)

const retainedCanObserveExit = true

func retainedAwaitExit(p *retainedCommand) error {
	return retainedBSDWait(p.cmd.Process.Pid)
}

func retainedSweep(cmd *exec.Cmd, _ func() error) error {
	pid := cmd.Process.Pid
	err := unix.Kill(-pid, unix.SIGKILL)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}
