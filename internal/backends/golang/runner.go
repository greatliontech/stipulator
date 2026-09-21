package golang

import (
	"errors"
	"os/exec"
	"time"

	"github.com/greatliontech/gofresh/gotool"
)

// quitGrace bounds the window between the envelope-expiry SIGQUIT and the
// process group's SIGKILL: long enough for the Go runtime to write a full
// goroutine dump, short enough that a group ignoring SIGQUIT cannot stall
// the run (the boundary's grace on every platform that has the signal).
const quitGrace = 10 * time.Second

// ownedBoundary is the owned process boundary as gofresh's containment
// (REQ-go-owned-processes): the child in its own process group, swept
// whole by the operation's cancellation, asked to quit first with the
// quit grace when the cancellation is the policy envelope's expiry (a
// test binary writes its goroutine dump on SIGQUIT), the reap bounded
// by the policy's wait delay.
var ownedBoundary = &gotool.Containment{
	Quit:  func(cause error) bool { return errors.Is(cause, errEnvelopeExpired) },
	Grace: quitGrace,
}

// ownedRunner is the derivation's go-command runner under the owned
// boundary: discovery's listings, the witness invocations, and the
// normalization's environment snapshot run through it, and the test
// seam (commandHook) sees each of those spawns — the derivation's own,
// which the reuse pins count. The resolver child — this binary's own
// re-exec, no go command — keeps the same boundary through
// commandContext, the one non-go spawn.
var ownedRunner = gotool.Runner{Containment: ownedBoundary, Prepare: observeCommand}

// engineRunner carries the owned boundary to the analysis engines'
// own go commands (gofresh.WithGoRunner on every engine) — gofresh's
// spawns, outside the derivation seam; engineCommandHook is the test
// seam that witnesses the installation, nil in production.
var engineRunner = gotool.Runner{Containment: ownedBoundary, Prepare: func(cmd *exec.Cmd) {
	if engineCommandHook != nil {
		engineCommandHook(cmd)
	}
}}

var engineCommandHook func(*exec.Cmd)

// probeRunner is the toolchain sample's runner: the caller's own
// process group — a descendant-free query, swept with its caller by the
// owner that kills the caller outright (the served resolver child's
// client does exactly that) — with the reap bounded so a wrapper's
// descendant holding the pipe cannot outlive the cancellation
// (REQ-policy-cancellation); a sample is no derivation spawn, so the
// seam does not see it.
var probeRunner = gotool.Runner{Prepare: boundProbe}

// boundProbe is probeRunner's preparation: the bounded reap alone.
func boundProbe(cmd *exec.Cmd) { cmd.WaitDelay = probeWaitDelay }

// observeCommand hands a prepared derivation spawn to the test seam
// (commandHook), the go tool's name and arguments as the seam reads
// them.
func observeCommand(cmd *exec.Cmd) {
	if commandHook != nil {
		commandHook("go", cmd.Args[1:])
	}
}
