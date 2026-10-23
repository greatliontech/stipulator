//go:build unix

package golang

import (
	"errors"
	"os/exec"

	"golang.org/x/sys/unix"
)

func retainedQuit(cmd *exec.Cmd) bool {
	err := unix.Kill(-cmd.Process.Pid, unix.SIGQUIT)
	return err == nil || errors.Is(err, unix.ESRCH)
}
