package golang

import (
	"context"
	"os/exec"
)

func commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommandCancellation(cmd)
	return cmd
}
