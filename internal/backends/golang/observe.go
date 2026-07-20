package golang

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
//
// Every completed observation is constructed against an observation
// bracket captured before its process spawned (gofresh
// REQ-inputs-value-binding), declaring the package's own directory —
// module-relative under the verification tree root — as the one bracket
// root. The package directory is the root policy here because it is the
// surface `go test` conventions give a test to read (testdata rides it as
// a subtree) while staying narrow enough that a sibling package's
// parallel writes cannot move it; declaring the whole tree instead would
// turn every concurrent package's writes into bracket noise. The
// consequence is deliberate: a read resolving outside the package
// directory seals per-identity unverifiable — permanently uncacheable —
// and a test wanting cacheable cross-directory fixtures must move them
// under its package or assert purity in source.

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

// observationFrame is the resolved frame one process's observation is
// captured and ingested under: the verification tree root and package
// directory with symlinks resolved, and the pre-spawn observation
// bracket — or, when no bracket exists, the fail-closed reason the
// process's incomplete observation carries. Capture and ingestion share
// the frame because gofresh refuses a bracket interpreted under a
// different module view than its capture.
type observationFrame struct {
	root   string
	pkgDir string
	// bracket is nil exactly when reason is non-empty.
	bracket *runtimeinput.Bracket
	reason  string
}

// captureObservationFrame captures the pre-spawn observation bracket one
// package's completed observation seals on: a fingerprint over the
// package directory, declared module-relative under the verification tree
// root — never absolute, whose directory form gofresh's hashing semantics
// refuse. Both the tree root and the package directory are
// symlink-resolved before the containment check and before framing,
// mirroring gofresh's own resolvedTarget: `go list` reports resolved
// directories, so under a symlinked tree prefix (macOS /tmp, a linked
// checkout) a lexical comparison would misclassify every package as
// outside the tree, and an unresolved module view would classify every
// recorded read as external — either way silently unbinding everything.
// Three shapes yield no bracket, each fail-closed to an incomplete
// observation carrying the returned reason: a package whose directory
// discovery did not resolve, one whose resolved directory lies outside
// the resolved verification tree (an external workspace member — no
// module-relative root can name it, so it is permanently uncacheable
// here), and a resolution or capture that errored outright. A capture
// that succeeds while gofresh's hashing semantics refuse a root still
// returns its bracket: gofresh seals that observation attributably
// unverifiable, which is the honest disposition — the process ran and
// its evidence exists, it just cannot bind. The VCS bookkeeping tree is
// excluded exactly as the observation's manifest excludes it, so a
// module-root package's bracket is not moved by unrelated tooling. The
// tree's own bookkeeping directory (`.stipulator`) is deliberately not
// excluded: every in-process writer of it runs after ingestion, so it
// cannot move an in-flight bracket, and an external tool writing it
// mid-run is exactly the interference a bracket exists to catch — a
// module-root package pays re-execution for it, never a wrong bind.
func captureObservationFrame(ctx context.Context, n *NormalizedInvocation, pkg string) observationFrame {
	pkgDir, ok := n.PkgDirs[pkg]
	if !ok {
		return observationFrame{reason: "package directory unknown at spawn; no observation bracket was captured"}
	}
	root, err := filepath.EvalSymlinks(treeRoot(n))
	if err != nil {
		return observationFrame{reason: fmt.Sprintf("observation bracket capture failed: %v", err)}
	}
	resolvedPkgDir, err := filepath.EvalSymlinks(pkgDir)
	if err != nil {
		return observationFrame{reason: fmt.Sprintf("observation bracket capture failed: %v", err)}
	}
	rel, err := filepath.Rel(root, resolvedPkgDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return observationFrame{reason: fmt.Sprintf("package directory %s lies outside the verification tree; no observation bracket can cover it", pkgDir)}
	}
	b, err := runtimeinput.CaptureBracketContext(ctx, root, []string{filepath.ToSlash(rel)},
		runtimeinput.WithBracketExcludedPaths(".git"))
	if err != nil {
		return observationFrame{reason: fmt.Sprintf("observation bracket capture failed: %v", err)}
	}
	return observationFrame{root: root, pkgDir: resolvedPkgDir, bracket: &b}
}

// observeProcess builds the observation one launched process owns, from
// its parsed stream, exit, terminal disposition, testlog capture, and
// pre-spawn observation frame. A missing bracket disqualifies exactly
// as an unproven flush does: without a pre-spawn fingerprint no completed
// observation can bind its values, so the record fails closed as
// incomplete with the frame's stated reason.
func observeProcess(n *NormalizedInvocation, pkg string, producer *stipulatorv1.ProducerIdentity, st *streamState, waitErr error, disposition stipulatorv1.HealthDisposition, logPath string, frame observationFrame) *ProcessObservation {
	reason := incompleteObservationReason(st, waitErr, disposition, logPath)
	if reason == "" && frame.bracket == nil {
		reason = frame.reason
	}
	if reason == "" {
		obs, err := completedObservation(n, pkg, producer, logPath, frame)
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
// gofresh under the resolved observation frame, asserting process
// completion against the pre-spawn observation bracket, with the declared
// repository-root and VCS exclusions every witness observation carries.
func completedObservation(n *NormalizedInvocation, pkg string, producer *stipulatorv1.ProducerIdentity, logPath string, frame observationFrame) (*ProcessObservation, error) {
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
	// The repository root listing and the VCS bookkeeping tree are asserted
	// to be no witness's input (their digests move under unrelated
	// tooling); the exclusion carries the caller-side soundness
	// responsibility gofresh's exclusion contract assigns it.
	opts := []runtimeinput.TestLogOption{
		runtimeinput.WithCompletedProcess(processIdentity(n, producer, pkg)),
		runtimeinput.WithBracket(*frame.bracket),
		runtimeinput.WithExcludedPaths(".", ".git"),
	}
	// Toolchain and module-cache reads classify guard-covered: the
	// toolchain guard pins the toolchain root's contents, module trees
	// are pinned by version-addressed immutability, so a test reading
	// GOROOT or a module tree stays cacheable instead of sealing
	// unverifiable. The module cache's download-cache subtree stays
	// observed - gofresh's classification carves it out.
	if n.ToolchainRoot != "" {
		opts = append(opts, runtimeinput.WithToolchainRoot(n.ToolchainRoot))
	}
	if n.ModuleCacheRoot != "" {
		opts = append(opts, runtimeinput.WithModuleCacheRoot(n.ModuleCacheRoot))
	}
	// The build cache is guard-covered on toolchain-mediated
	// observational equivalence — the toolchain rederives or revalidates
	// cache content from inputs the fingerprint already pins; a subject
	// consuming cache objects as data is outside the stated assumption —
	// and the temp root is ephemeral (only its own identity admits;
	// deeper reads stay observed).
	if n.BuildCacheRoot != "" {
		opts = append(opts, runtimeinput.WithBuildCacheRoot(n.BuildCacheRoot))
	}
	if n.TempRoot != "" {
		opts = append(opts, runtimeinput.WithEphemeralTempRoot(n.TempRoot))
	}
	observation, err := runtimeinput.FromTestLogEnv(log, frame.root, frame.pkgDir, n.Env, opts...)
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
