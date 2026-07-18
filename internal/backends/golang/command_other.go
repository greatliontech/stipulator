//go:build !unix && !windows

package golang

import (
	"context"
	"os/exec"
)

// No process-group ownership here, and no SIGQUIT-first escalation on
// envelope expiry: a timed-out child dies without a goroutine dump.
func configureCommandCancellation(_ context.Context, cmd *exec.Cmd) {}
