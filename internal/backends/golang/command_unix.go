//go:build unix

package golang

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureCommandCancellation places the one non-go spawn — the
// resolver child, this binary re-executed — in its own process group
// and kills the group outright when its context ends. The child's
// context is the operation's, never a package envelope's, so the
// envelope-expiry quit (a goroutine dump before the kill) the go
// children's containment carries (ownedBoundary) has no arm here: an
// ended operation discards the child's answers whole, and a dump would
// have no consumer.
func configureCommandCancellation(ctx context.Context, cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
