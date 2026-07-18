//go:build unix

package golang

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// quitGrace bounds the window between the envelope-expiry SIGQUIT and the
// process group's SIGKILL: long enough for the Go runtime to write a full
// goroutine dump, short enough that a group ignoring SIGQUIT cannot stall
// the run.
const quitGrace = 10 * time.Second

func configureCommandCancellation(ctx context.Context, cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		pgid := -cmd.Process.Pid
		if errors.Is(context.Cause(ctx), errEnvelopeExpired) {
			// Envelope expiry is a reported terminal fact, so the kill must
			// leave evidence: SIGQUIT makes a Go child print its goroutine
			// dump before exiting — the only dump path for a binary wedged
			// before m.Run arms the reviewed -test.timeout timer, now that
			// the go command's implicit backstop is disabled — then SIGKILL
			// sweeps the group after a bounded grace. Every other ending —
			// client cancellation and a caller's own deadline alike — skips
			// the dump and kills outright: those runs are discarded whole,
			// so a dump would have no consumer and the grace would only
			// delay the abort.
			if err := syscall.Kill(pgid, syscall.SIGQUIT); errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			deadline := time.Now().Add(quitGrace)
			for time.Now().Before(deadline) {
				if errors.Is(syscall.Kill(pgid, 0), syscall.ESRCH) {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		err := syscall.Kill(pgid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
