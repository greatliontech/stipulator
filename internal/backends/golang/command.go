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

// commandHook observes every spawn that goes through commandContext —
// the toolchain queries, listings, and test runs of normalization,
// discovery, and execution; the provenance probe (goVersionCmd, a
// descendant-free query that stays in its caller's process group) and
// the Windows job wrapper spawn on their own. Tests install it to pin
// that a refusal fired before any of these spawns.
var commandHook func(name string, args []string)

func commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	if commandHook != nil {
		commandHook(name, args)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommandCancellation(ctx, cmd)
	return cmd
}
