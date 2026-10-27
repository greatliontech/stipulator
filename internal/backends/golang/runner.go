package golang

import (
	"errors"
	"os/exec"
	"sync"
	"time"

	"github.com/greatliontech/gofresh/gotool"
)

// errEnvelopeExpired marks a context whose deadline is a policy
// invocation's reviewed envelope: the one expiry whose kill must leave
// dump evidence, as opposed to a caller's own deadline, which discards
// the run whole.
var errEnvelopeExpired = errors.New("policy invocation envelope expired")

// commandHook observes the derivation's spawns — the normalization's
// snapshot, discovery listings, retained compilation and consumer
// binaries. The provenance probe and native sessions' analysis and
// classification-root commands are no derivation spawn and never
// reach it. Tests install it to pin that a refusal fired before any of
// these spawns and that the readers reuse the derivation.
var (
	commandHookMu sync.Mutex
	commandHook   func(name string, args []string)
)

// quitGrace bounds the window between the envelope-expiry SIGQUIT and the
// process group's SIGKILL: long enough for the Go runtime to write a full
// goroutine dump, short enough that a group ignoring SIGQUIT cannot stall
// the run (the boundary's grace on every platform that has the signal).
const quitGrace = 10 * time.Second

// ownedBoundary is the owned process boundary as gofresh's containment
// (REQ-go-owned-processes-runner): the child in its own process group, swept
// whole by the operation's cancellation, asked to quit first with the
// quit grace when the cancellation is the policy envelope's expiry (a
// test binary writes its goroutine dump on SIGQUIT), the reap bounded
// by the policy's wait delay.
var ownedBoundary = &gotool.Containment{
	Quit:  func(cause error) bool { return errors.Is(cause, errEnvelopeExpired) },
	Grace: quitGrace,
}

// ownedRunner is the derivation's go-command runner under the owned
// boundary: discovery's listings, resolution's package inventories, the
// witness invocations, and the normalization's environment snapshot run
// through it, and the test seam (commandHook) sees each of those spawns.
var ownedRunner = gotool.Runner{Containment: ownedBoundary, Prepare: observeCommand}

// observeChild hands a prepared consumer-program spawn to the test seam
// (commandHook): the program as named, then its arguments.
func observeChild(cmd *exec.Cmd) {
	commandHookMu.Lock()
	defer commandHookMu.Unlock()
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

// probeRunner is the toolchain sample's runner: the caller's own
// process group — `go env GOVERSION` names its key, so the query forks
// nothing and is swept with its caller when the caller is terminated —
// with the reap bounded so a wrapper's descendant holding the
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

// probeObserverForTest sees the provenance sampler's bounded probes. Native
// classification-root probes belong to the session's installed engine runner.
var probeObserverForTest func(*exec.Cmd)

// observeCommand hands a prepared derivation spawn to the test seam
// (commandHook), the go tool's name and arguments as the seam reads
// them.
func observeCommand(cmd *exec.Cmd) {
	commandHookMu.Lock()
	defer commandHookMu.Unlock()
	if commandHook != nil {
		commandHook("go", cmd.Args[1:])
	}
}
