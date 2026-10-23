package golang

import (
	"errors"
	"os/exec"

	"golang.org/x/sys/unix"
)

const retainedCanObserveExit = true

func retainedAwaitExit(p *retainedCommand) error {
	pid := p.cmd.Process.Pid
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

func retainedSweep(cmd *exec.Cmd, _ func() error) error {
	pid := cmd.Process.Pid
	err := unix.Kill(-pid, unix.SIGKILL)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}
