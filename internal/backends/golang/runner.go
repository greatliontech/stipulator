package golang

import (
	"errors"
	"os/exec"
	"time"

	"github.com/greatliontech/gofresh/gotool"
)

// errEnvelopeExpired marks a context whose deadline is a policy
// invocation's reviewed envelope: the one expiry whose kill must leave
// dump evidence, as opposed to a caller's own deadline, which discards
// the run whole.
var errEnvelopeExpired = errors.New("policy invocation envelope expired")

// commandHook observes the derivation's spawns — the normalization's
// snapshot, discovery's listings, and execution's test runs through
// ownedRunner, and the resolver child through childRunner; the
// provenance probe, the observation facade's roots probe and the
// analysis engines' own commands are no derivation spawn and never
// reach it. Tests install it to pin that a refusal fired before any of
// these spawns and that the readers reuse the derivation.
var commandHook func(name string, args []string)

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
// re-exec, no go command — is the one non-go spawn, through
// childRunner: the envelope-expiry quit above is the go children's
// alone.
var ownedRunner = gotool.Runner{Containment: ownedBoundary, Prepare: observeCommand}

// childRunner spawns the resolver child — this binary re-executed on
// its resolver route — as the consumer-command form of the one
// go-command policy (gotool.Runner.Program, REQ-go-owned-processes):
// its own process group, killed outright when its operation ends (the
// child's context is the operation's, never a package envelope's, so
// the envelope-expiry quit has no arm here — an ended operation
// discards the child's answers whole, and a dump would have no
// consumer), the reap bounded by the policy's wait delay, the parent's
// own environment normalized under the policy. The derivation seam sees
// the spawn as the child's program name with its arguments.
var childRunner = gotool.Runner{Containment: &gotool.Containment{}, Prepare: observeChild}

// observeChild hands the prepared resolver-child spawn to the test seam
// (commandHook): the program as named, then its arguments.
func observeChild(cmd *exec.Cmd) {
	if commandHook != nil {
		commandHook(cmd.Args[0], cmd.Args[1:])
	}
}

// containedRunner is a go-command runner under the owned boundary for
// spawns outside the derivation seam — the analysis engines' and the
// observation facade's — each with its own preparation (its test seam,
// its reap).
func containedRunner(prepare func(*exec.Cmd)) gotool.Runner {
	return gotool.Runner{Containment: ownedBoundary, Prepare: prepare}
}

// engineRunner carries the owned boundary to the analysis engines'
// own go commands (gofresh.WithGoRunner on every engine) — gofresh's
// spawns, outside the derivation seam; engineCommandHook is the test
// seam that witnesses the installation, nil in production.
var engineRunner = containedRunner(func(cmd *exec.Cmd) {
	if engineCommandHook != nil {
		engineCommandHook(cmd)
	}
})

var engineCommandHook func(*exec.Cmd)

// rootsRunner is the observation facade's classification-root probe's
// runner (runtimeinput.ProducerIngest.Runner): a bare `go env -json`
// in the package directory, which forks the C compiler the toolchain
// configures to read its flags — a descendant the owned boundary
// sweeps with the operation's cancellation, where a plain runner would
// orphan a hanging compiler wrapper — prepared as a probe (the bounded
// reap, the probe seam); no derivation spawn, so the derivation seam
// does not see it.
var rootsRunner = containedRunner(boundProbe)

// probeRunner is the toolchain sample's runner: the caller's own
// process group — `go env GOVERSION` names its key, so the query forks
// nothing and is swept with its caller by the owner that kills the
// caller outright (the served resolver child's client does exactly
// that) — with the reap bounded so a wrapper's descendant holding the
// pipe cannot outlive the cancellation (REQ-policy-cancellation); a
// sample is no derivation spawn, so the seam does not see it.
var probeRunner = gotool.Runner{Prepare: boundProbe}

// boundProbe is the probes' preparation: the bounded reap — and the
// probe seam, when a pin set one.
func boundProbe(cmd *exec.Cmd) {
	cmd.WaitDelay = probeWaitDelay
	if probeObserverForTest != nil {
		probeObserverForTest(cmd)
	}
}

// probeObserverForTest, when set, sees every probe prepared by
// boundProbe — the sampler's toolchain probe and the facade's roots
// probe — the seam a pin counts them through; nil in production.
var probeObserverForTest func(*exec.Cmd)

// observeCommand hands a prepared derivation spawn to the test seam
// (commandHook), the go tool's name and arguments as the seam reads
// them.
func observeCommand(cmd *exec.Cmd) {
	if commandHook != nil {
		commandHook("go", cmd.Args[1:])
	}
}
