package golang

import (
	"context"
	"errors"
	"os/exec"
)

// errEnvelopeExpired marks a context whose deadline is a policy
// invocation's reviewed envelope: the one expiry whose kill must leave
// dump evidence, as opposed to a caller's own deadline, which discards
// the run whole.
var errEnvelopeExpired = errors.New("policy invocation envelope expired")

func commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommandCancellation(ctx, cmd)
	return cmd
}
