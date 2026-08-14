package golang

import (
	"context"
	"fmt"
	"strconv"

	"github.com/greatliontech/gofresh/runtimeinput"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// Per-process runtime-input observation for the policy executor. Every
// launched test process owns exactly one observation, bound to its
// ProducerIdentity: the process's `-test.testlogfile` capture is ingested
// through gofresh's producer facade into a completed observation only
// when the process provably completed and flushed it — a healthy package
// disposition whose stream shows a terminal pass, no abort output, and no
// started-but-unfinished test. Anything short of that proof yields an
// incomplete observation carrying only its reason, never manifest bytes.
// Observations from distinct processes are never merged here; a union is
// a downstream consumer's judgment under its own contract.
//
// The facade owns frame capture (symlink resolution, containment, the
// bracket, the VCS exclusion), the test-log-header proof, the ingest
// exclusions, the PWD requirement, and the fail-closed fold discipline
// (gofresh REQ-inputs-producer-facade). What stays here is stipulator's
// vocabulary: which directory is the bracket root, and what counts as a
// provably completed process.
//
// The package directory is the bracket root policy here because it is
// the surface `go test` conventions give a test to read (testdata rides
// it as a subtree) while staying narrow enough that a sibling package's
// parallel writes cannot move it; declaring the whole tree instead would
// turn every concurrent package's writes into bracket noise. The
// consequence is deliberate: a read resolving outside the declared roots
// seals per-identity unverifiable — permanently uncacheable — and a test
// wanting cacheable cross-directory fixtures must move them under its
// package, name them in the invocation's reviewed bracket paths, or
// assert purity in source. The tree's own bookkeeping directory
// (`.stipulator`) is deliberately not excluded from the bracket: every
// in-process writer of it runs after ingestion, so it cannot move an
// in-flight bracket, and an external tool writing it mid-run is exactly
// the interference a bracket exists to catch — a module-root package
// pays re-execution for it, never a wrong bind.

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

// observationFrame is one process's producer frame plus the one refusal
// the facade cannot know: a package whose directory discovery never
// resolved has nothing to capture a frame for, so the spawn-side reason
// rides beside the zero frame and reaches the facade as the caller's
// incompleteness verdict.
type observationFrame struct {
	frame runtimeinput.ProducerFrame
	// spawnReason is non-empty exactly when no capture was attempted.
	spawnReason string
}

// captureObservationFrame captures the pre-spawn frame one package's
// completed observation seals on: the package directory declared
// module-relative under the verification tree root, beside the
// invocation's reviewed bracket paths — fingerprinted pre-spawn, present
// or absent, so consumed process images and fixed external files bind
// instead of sealing out-of-bracket. Resolution, containment, and the
// capture refusals are the facade's (an external workspace member — no
// module-relative root can name it — is permanently uncacheable here). A
// capture that succeeds while gofresh's hashing semantics refuse a root
// still returns its bracket: gofresh seals that observation attributably
// unverifiable, which is the honest disposition — the process ran and
// its evidence exists, it just cannot bind.
func captureObservationFrame(ctx context.Context, n *NormalizedInvocation, pkg string) observationFrame {
	pkgDir, ok := n.PkgDirs[pkg]
	if !ok {
		return observationFrame{spawnReason: "package directory unknown at spawn; no observation bracket was captured"}
	}
	// The invocation's reviewed exclusions apply at both endpoints: a
	// path whose manifest reads the exclusion drops ("its digest moves
	// under unrelated tooling") must not churn the bracket either, or
	// the same unrelated tooling seals the observation the exclusion
	// exists to keep cacheable. The caller-side soundness assertion the
	// exclusion carries covers both uses.
	return observationFrame{frame: runtimeinput.CaptureProducerFrame(ctx, treeRoot(n), pkgDir,
		runtimeinput.FrameOptions{BracketPaths: n.BracketPaths, ExcludedPaths: n.ExcludedPaths})}
}

// observeProcess builds the observation one launched process owns, from
// its parsed stream, exit, terminal disposition, testlog capture, and
// pre-spawn observation frame. Stipulator's process-health verdict — and
// the spawn-side refusal of a package with no known directory — enter
// the facade as the caller's incompleteness verdict; every other
// fail-closed shape (missing, unreadable, or headerless capture, a
// bracketless frame, a PWD mismatch, an ingestion failure) is the
// facade's own fold. A construction error out of the facade (a cancelled
// context, a malformed environment) still yields the fail-closed record:
// the run it belongs to is discarded or diagnosed upstream, never bound.
func observeProcess(ctx context.Context, n *NormalizedInvocation, pkg string, producer *stipulatorv1.ProducerIdentity, st *streamState, waitErr error, disposition stipulatorv1.HealthDisposition, logPath string, frame observationFrame) *ProcessObservation {
	callerReason := incompleteObservationReason(st, waitErr, disposition)
	if callerReason == "" {
		callerReason = frame.spawnReason
	}
	observation, reason, err := frame.frame.Observe(ctx, logPath, runtimeinput.ProducerIngest{
		Identity:         processIdentity(n, producer, pkg),
		Env:              witnessProcessEnv(n, frame),
		IncompleteReason: callerReason,
		Roots: runtimeinput.ClassificationRoots{
			Toolchain:     n.ToolchainRoot,
			ModuleCache:   n.ModuleCacheRoot,
			BuildCache:    n.BuildCacheRoot,
			EphemeralTemp: n.TempRoot,
		},
		ExcludedPaths: n.ExcludedPaths,
	})
	if err != nil {
		return incompleteObservation(pkg, producer, fmt.Sprintf("observation construction failed: %v", err))
	}
	if reason != "" {
		return incompleteObservation(pkg, producer, reason)
	}
	state, err := runtimeinput.CompletedState(observation)
	if err != nil {
		return incompleteObservation(pkg, producer, fmt.Sprintf("testlog ingestion failed: %v", err))
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
	return &ProcessObservation{Wire: wire, Runtime: observation}
}

// incompleteObservationReason is stipulator's process-health verdict. It
// returns the empty string only when the producing process provably
// completed and flushed its testlog; otherwise it names the first
// disqualifying fact, in a fixed order so the same stream always yields
// the same reason. Capture-file health (attached, present, readable,
// headed) is the facade's judgment, not this one.
func incompleteObservationReason(st *streamState, waitErr error, disposition stipulatorv1.HealthDisposition) string {
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

// witnessProcessEnv is the frozen environment one package's witness
// process actually runs under: the invocation environment with PWD
// pinned to the package directory the test binary starts in, so a PWD
// read is truthful — fully determined by the frame identity — and
// admits recordless instead of sealing process-local. Spawn and
// ingestion MUST use the same environment; this helper is the single
// source for both, and the facade's ingest refuses an environment whose
// PWD does not name the frame's package directory. The go tool
// independently appends the same PWD=<package dir> when it spawns the
// test binary, so the spawn-side pin is deliberately redundant: it keeps
// the ingest mirror sound even if a future toolchain stopped supplying
// it, which is why no test can distinguish dropping the spawn-side call
// alone.
func witnessProcessEnv(n *NormalizedInvocation, frame observationFrame) []string {
	env := witnessWidthEnv(n)
	if frame.frame.PkgDir == "" {
		return env
	}
	return setEnv(env, "PWD", frame.frame.PkgDir)
}

// witnessWidthEnv applies the unit's inner-parallelism cap to the
// frozen invocation environment as GOMAXPROCS - the one entry the go
// tool's build workers and -p default, and the test binary's scheduler
// and -parallel default, all honor; an explicit flag is deliberately
// not emitted because it would override an environment bound the
// default never does. The cap only ever narrows: an environment
// already carrying a narrower positive GOMAXPROCS keeps it. Feeding
// the single spawn-and-ingest source makes the injected value part of
// the recorded observation environment by construction: a witness that
// reads GOMAXPROCS records the value its process actually saw, and
// exactly those witnesses re-execute when the width moves with the
// unit bound - a mirror hiding the injection would serve stale
// verdicts to width-sensitive witnesses instead.
func witnessWidthEnv(n *NormalizedInvocation) []string {
	width := witnessChildWidth(n)
	if v, ok := lookupEnv(n.Env, "GOMAXPROCS"); ok {
		if ambient, err := strconv.Atoi(v); err == nil && ambient > 0 && ambient <= width {
			return n.Env
		}
	}
	return setEnv(n.Env, "GOMAXPROCS", strconv.Itoa(width))
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
	rc.SetPlainWitness(n.PlainWitness)
	return rc
}
