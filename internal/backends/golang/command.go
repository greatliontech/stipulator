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

// commandHook observes the derivation's spawns — the normalization's
// snapshot, discovery's listings, and execution's test runs through
// ownedRunner (runner.go), and the resolver child through
// commandContext; the provenance probe and the analysis engines' own
// commands are no derivation spawn and never reach it. Tests install it
// to pin that a refusal fired before any of these spawns and that the
// readers reuse the derivation.
var commandHook func(name string, args []string)

// commandContext spawns the resolver child — this binary's own re-exec,
// no go command, so outside gotool's runner — under the same owned
// boundary (configureCommandCancellation).
func commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	if commandHook != nil {
		commandHook(name, args)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommandCancellation(ctx, cmd)
	return cmd
}
