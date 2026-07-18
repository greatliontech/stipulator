package golang

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/greatliontech/gofresh/runtimeinput"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// testlogHeader is the first line the testing runtime writes on opening
// its `-test.testlogfile` capture; its presence proves the binary opened
// the file.
var testlogHeader = []byte("# test log")

// Per-process runtime-input observation for the policy executor. Every
// launched test process owns exactly one observation, bound to its
// ProducerIdentity: the process's `-test.testlogfile` capture is ingested
// through gofresh into a completed observation only when the process
// provably completed and flushed it — a healthy package disposition whose
// stream shows a terminal pass, no abort output, and no started-but-
// unfinished test. Anything short of that proof yields an incomplete
// observation carrying only its reason, never manifest bytes: a process
// that dies mid-log loses or truncates the flush, and a lost read must
// fail closed as "no completed evidence", never masquerade as gofresh's
// "no runtime inputs observed" assertion. Observations from distinct
// processes are never merged here; a union is a downstream consumer's
// judgment under its own contract.

// ProcessObservation pairs one launched process's wire observation with
// the live gofresh evidence behind it. Runtime is set exactly when Wire
// carries a completed record: gofresh's producer-side attach path consumes
// the sealed observation value, which has no wire decode, so the live
// value must survive in memory beside the report for in-process
// consumers. An incomplete record retains no live value — there is no
// evidence to hand anywhere.
type ProcessObservation struct {
	Wire    *stipulatorv1.Observation
	Runtime runtimeinput.Observation
}

// observeProcess builds the observation one launched process owns, from
// its parsed stream, exit, terminal disposition, and testlog capture.
func observeProcess(ctx context.Context, n *NormalizedInvocation, pkg string, producer *stipulatorv1.ProducerIdentity, st *streamState, waitErr error, disposition stipulatorv1.HealthDisposition, logPath string) *ProcessObservation {
	reason := incompleteObservationReason(st, waitErr, disposition, logPath)
	if reason == "" {
		obs, err := completedObservation(ctx, n, pkg, producer, logPath)
		if err == nil {
			return obs
		}
		reason = fmt.Sprintf("testlog ingestion failed: %v", err)
	}
	return incompleteObservation(pkg, producer, reason)
}

// incompleteObservationReason decides completeness. It returns the empty
// string only when the producing process provably completed and flushed
// its testlog; otherwise it names the first disqualifying fact, in a fixed
// order so the same stream always yields the same reason.
func incompleteObservationReason(st *streamState, waitErr error, disposition stipulatorv1.HealthDisposition, logPath string) string {
	if logPath == "" {
		return "testlog capture unavailable: no capture file was attached to the process"
	}
	if disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		return fmt.Sprintf("package disposed %s, not HEALTHY; the testlog flush is unproven", disposition)
	}
	if st.terminal != "pass" {
		// The healthy non-pass terminal is "skip": no test binary ran (no
		// test files), so no process observed anything.
		return "no test process ran (terminal " + st.terminal + ")"
	}
	if st.sawAbort {
		return "abort output observed in the stream; the testlog flush cannot be trusted"
	}
	if names := startedTests(st); len(names) > 0 {
		return "tests started but unfinished; the process died before its testlog flushed"
	}
	// Defense in depth: classifyRun grants HEALTHY only under a nil
	// waitErr, so no production path reaches this branch — it guards the
	// conjunction's own completeness, not a reachable state.
	if waitErr != nil {
		return fmt.Sprintf("process exited with failure: %v", waitErr)
	}
	return ""
}

// completedObservation ingests one completed process's testlog through
// gofresh, asserting process completion, with the declared repository-root
// and VCS exclusions every witness observation carries.
func completedObservation(ctx context.Context, n *NormalizedInvocation, pkg string, producer *stipulatorv1.ProducerIdentity, logPath string) (*ProcessObservation, error) {
	log, err := os.ReadFile(logPath)
	if err != nil {
		return nil, fmt.Errorf("reading testlog: %w", err)
	}
	// The testing runtime writes the "# test log" header the moment it
	// opens its capture file, so the header is the proof the binary opened
	// this file at all. Without it the capture is the executor's own
	// untouched temp file — a TestMain that exits without m.Run passes
	// cleanly yet never opens the capture — and ingesting those zero bytes
	// would seal "no runtime inputs observed" over a run that observed
	// freely.
	if !bytes.HasPrefix(log, testlogHeader) {
		return nil, fmt.Errorf("capture file carries no test-log header; the test binary never opened it")
	}
	pkgDir, ok := packageDir(ctx, n.Dir, n.Env, pkg)
	if !ok {
		return nil, fmt.Errorf("resolving package directory for %s", pkg)
	}
	// The repository root listing and the VCS bookkeeping tree are asserted
	// to be no witness's input (their digests move under unrelated
	// tooling); the exclusion carries the caller-side soundness
	// responsibility gofresh's exclusion contract assigns it.
	observation, err := runtimeinput.FromTestLogEnv(log, treeRoot(n), pkgDir, n.Env,
		runtimeinput.WithCompletedProcess(processIdentity(n, producer, pkg)),
		runtimeinput.WithExcludedPaths(".", ".git"))
	if err != nil {
		return nil, err
	}
	state, err := runtimeinput.CompletedState(observation)
	if err != nil {
		return nil, err
	}
	completed := &stipulatorv1.CompletedObservation{}
	completed.SetManifest(state.Manifest)
	if !state.Unverifiable {
		completed.SetDigest(state.Digest)
	}
	wire := &stipulatorv1.Observation{}
	wire.SetProducer(producer)
	wire.SetPackage(pkg)
	wire.SetCompleted(completed)
	return &ProcessObservation{Wire: wire, Runtime: observation}, nil
}

// incompleteObservation is the fail-closed record: a launched process
// whose testlog flush is unproven owns an observation carrying only its
// reason.
func incompleteObservation(pkg string, producer *stipulatorv1.ProducerIdentity, reason string) *ProcessObservation {
	wire := &stipulatorv1.Observation{}
	wire.SetProducer(producer)
	wire.SetPackage(pkg)
	wire.SetIncompleteReason(reason)
	return &ProcessObservation{Wire: wire}
}

// processIdentity names one launched process for gofresh's process
// provenance: unique within the execution (the spawn ordinal
// disambiguates pid reuse) and stable for the same process.
func processIdentity(n *NormalizedInvocation, producer *stipulatorv1.ProducerIdentity, pkg string) string {
	return fmt.Sprintf("%s#%d:%s", n.Name, producer.GetProcessOrdinal(), pkg)
}

// resolvedConfig renders the invocation's resolved pin-at-load
// configuration for its evidentiary record: what actually ran, reviewable
// after the fact.
func resolvedConfig(n *NormalizedInvocation) *stipulatorv1.GoResolvedConfig {
	rc := &stipulatorv1.GoResolvedConfig{}
	rc.SetToolchain(n.Toolchain)
	rc.SetGoos(n.GOOS)
	rc.SetGoarch(n.GOARCH)
	rc.SetCgoEnabled(n.CgoEnabled)
	rc.SetGoflags(n.GOFLAGS)
	rc.SetGoexperiment(n.GOEXPERIMENT)
	rc.SetWorkspaceOn(n.WorkspaceOn)
	rc.SetRace(n.Race)
	return rc
}
