package golang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/greatliontech/gofresh/resident"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
	"google.golang.org/protobuf/types/known/durationpb"
)

// The policy executor runs each normalized Go invocation exactly once and
// derives terminal health from retained binaries' actual exits and their owned
// output conversion streams. Per-package
// processes are what make attribution honest: every outcome in the report
// names the one process whose stream produced it (REQ-policy-attribution),
// and per-process observation refines the same boundary. The
// executor trusts nothing silent: a stream that ends without a terminal
// package or build event, carries unparseable bytes, or disagrees with its process
// exit status is degraded, never healthy — an environment that swallowed a
// suite must be distinguishable from a suite that passed.

// failureOutputCap bounds the retained output of one failure diagnostic.
// Retention is part of the verdict; the cap keeps a pathological stream
// (a runaway goroutine dump, a looping test) from turning the report into
// the log it summarizes. Truncation is always marked, never silent.
const failureOutputCap = 64 << 10

// isAbortOutput recognizes the output of a dying test binary. A test that
// legitimately prints these words costs a spurious untrusted-stream
// classification for its process — its evidence is refused, nothing more.
func isAbortOutput(s string) bool {
	return strings.Contains(s, "panic: ") || strings.Contains(s, "fatal error: ")
}

// binaryTimeoutRe recognizes the test binary's own deadline panic — the one
// shape the testing runtime prints when -test.timeout expires. It is detection
// only: the bound the diagnostic names comes from the reviewed record
// (REQ-policy-explicit-time-bounds — the record's envelope and its reviewed
// arguments are the only sources of execution bounds), and a run whose record
// declares no binary bound is never reclassified, so a test printing this line
// can at worst relabel a package that is already red under a declared bound. A
// green stream is never reclassified — the recognition feeds classification of
// a terminal fail alone.
var binaryTimeoutRe = regexp.MustCompile(`^panic: test timed out after (\S+)`)

// timeoutRosterRe matches one entry of the deadline panic's own
// "running tests:" roster — the testing runtime's account of which
// subjects the deadline cut off. The entry shape (two tabs, a name, a
// space, a parenthesized elapsed time) does not collide with goroutine
// dump frames, whose call lines carry no space before the parenthesis.
var timeoutRosterRe = regexp.MustCompile(`^\t\t(\S+) \(`)

// remainderShare bounds the unparsed stream remainder a diagnostic
// renders: a quarter of the cap identifies the poison and leaves the
// package output its room.
const remainderShare = failureOutputCap / 4

// rosterShare bounds the running/aborted test roster a timeout diagnostic
// renders in its head so a large roster never starves the retained output
// that follows it.
const rosterShare = failureOutputCap / 4

// formatBoundedRoster renders a comma-separated test roster within
// rosterShare bytes, appending "(+N more omitted)" when entries are dropped.
func formatBoundedRoster(lead string, names []string) (string, bool) {
	if len(names) == 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString(lead)
	budget := rosterShare - len(lead)
	if budget < 0 {
		budget = 0
	}
	used := 0
	included := 0
	truncated := false
	for i, name := range names {
		sep := ""
		if i > 0 {
			sep = ", "
		}
		if used+len(sep)+len(name) > budget {
			if i == 0 {
				cut := cutAtRune(name, budget)
				b.WriteString(cut)
				included = 1
				truncated = true
			}
			break
		}
		b.WriteString(sep)
		b.WriteString(name)
		used += len(sep) + len(name)
		included++
	}
	if omitted := len(names) - included; omitted > 0 {
		fmt.Fprintf(&b, " (+%d more omitted)", omitted)
		truncated = true
	}
	return b.String(), truncated
}

// boundedBuffer retains at most failureOutputCap bytes and records that it
// dropped the rest.
type boundedBuffer struct {
	b         strings.Builder
	truncated bool
}

func (bb *boundedBuffer) write(s string) {
	room := failureOutputCap - bb.b.Len()
	if room <= 0 {
		bb.truncated = bb.truncated || s != ""
		return
	}
	if len(s) > room {
		// The cap cut backs off to a rune boundary: process output is
		// bytes, and a mid-rune cut would hand a proto string field the
		// invalid UTF-8 its marshal validation refuses.
		s = cutAtRune(s, room)
		bb.truncated = true
	}
	bb.b.WriteString(s)
}

// cutAtRune is the one byte-limit cut over text: at most limit bytes
// (none for a limit at or below zero), backed off to a rune boundary
// so the cut never splits a character — the cap's, the remainder
// share's, and the environment report's.
func cutAtRune(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

func (bb *boundedBuffer) empty() bool { return bb.b.Len() == 0 && !bb.truncated }

// testEvent is the subset of test2json (and go build -json) output the
// executor reads: the action, the test it names, its output, and on a
// terminal fail event the package whose compilation failed. The
// package fields both event kinds carry are not read.
type testEvent struct {
	Time        time.Time
	Action      string
	Test        string
	Elapsed     float64
	Output      string
	FailedBuild string
}

// packageRun is one selected package's parsed execution. A zero
// disposition means the run reached no terminal fact of its own — the
// caller classifies it as timeout or discards it on cancellation.
// aborted carries the names of tests that had started but not finished
// when the process ended — whether the envelope cut it off or the
// package died under them with a terminal verdict — and residue the
// bounded output a cut-off process left behind. producer is set exactly
// when a process was launched; obs is that process's owned observation,
// absent until the caller classifies a cut-off run.
type packageRun struct {
	pkg       string
	execution *packageExecution
	attempt   *nativeAttempt
	// soloTest names the one test an isolation re-run executed; empty
	// for a package's own process.
	soloTest    string
	disposition stipulatorv1.HealthDisposition
	aborted     []string
	residue     *boundedBuffer
	producer    *stipulatorv1.ProducerIdentity
	obs         *ProcessObservation
	tests       []*stipulatorv1.TestResult
	diags       []*stipulatorv1.FailureDiagnostic
	// peakBytes is the largest resident set any process of the
	// package's tree reached, from its wait status (0 where the host
	// answers none) — the admission's completed-package evidence.
	peakBytes uint64
	// heldBy carries the memory term's words when the package never
	// spawned because the term held it until the invocation's end.
	heldBy          string
	admissionOrigin string
	queueWait       time.Duration
	buildElapsed    time.Duration
	binaryElapsed   time.Duration
	totalElapsed    time.Duration
	noTestFiles     bool
	testTimings     []*stipulatorv1.TestTimingDiagnostic
	timing          *stipulatorv1.PackageTimingDiagnostic
}

func (r *packageRun) buildTimingDiagnostic(invocation string, cancelled bool) *stipulatorv1.PackageTimingDiagnostic {
	if r.timing != nil {
		return r.timing
	}
	pt := &stipulatorv1.PackageTimingDiagnostic{}
	pt.SetInvocation(invocation)
	pt.SetPackage(r.pkg)
	if r.soloTest != "" {
		pt.SetSoloTest(r.soloTest)
	}
	pt.SetQueueWait(durationpb.New(r.queueWait))
	if r.admissionOrigin != "" {
		pt.SetAdmissionOrigin(r.admissionOrigin)
	} else if r.heldBy != "" {
		pt.SetAdmissionOrigin(r.heldBy)
	}
	pt.SetBuildElapsed(durationpb.New(r.buildElapsed))
	pt.SetBinaryElapsed(durationpb.New(r.binaryElapsed))
	pt.SetTotalElapsed(durationpb.New(r.totalElapsed))
	pt.SetTests(r.testTimings)
	var state stipulatorv1.PackageExecutionState
	switch {
	case r.producer == nil && r.buildElapsed == 0 && (cancelled || r.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_UNSPECIFIED || r.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT || r.heldBy != ""):
		state = stipulatorv1.PackageExecutionState_PACKAGE_EXECUTION_STATE_NEVER_STARTED
	case cancelled || r.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_UNSPECIFIED || r.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT:
		state = stipulatorv1.PackageExecutionState_PACKAGE_EXECUTION_STATE_CANCELLED
	case r.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY && r.noTestFiles:
		state = stipulatorv1.PackageExecutionState_PACKAGE_EXECUTION_STATE_SKIPPED
	case r.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY:
		state = stipulatorv1.PackageExecutionState_PACKAGE_EXECUTION_STATE_COMPLETED
	default:
		state = stipulatorv1.PackageExecutionState_PACKAGE_EXECUTION_STATE_FAILED
	}
	pt.SetState(state)
	r.timing = pt
	return pt
}

// ExecuteInvocation executes one normalized invocation's selected packages
// — the package obligations of selection — each in its own owned,
// cancellable retained test process, fanned out under the derived
// concurrency bound with the invocation's reviewed
// envelope timeout governing the whole invocation as a context deadline.
// Every selected package executes whole: the exported executor accepts
// no test selection, so the health-judged path is structurally unable to
// narrow — health is a property of the entire declared invocation
// (REQ-core-one-execution), and witness-only narrowing lives behind
// ExecuteSelection, which never grants health.
// It returns the invocation's terminal health — carrying the resolved
// pin-at-load configuration as its evidentiary record — with every
// selected package disposed, the named test outcomes attributed to their
// producing process, bounded failure diagnostics, and one owned
// observation per launched process. Caller cancellation discards the
// partial run: the return is (nil, nil, nil, nil, ctx.Err()), never a
// partial report (REQ-policy-cancellation). Envelope expiry is not
// cancellation — it is a terminal fact, reported as TIMEOUT dispositions
// with each cut-off launched process owning an incomplete observation.
func ExecuteInvocation(ctx context.Context, n *NormalizedInvocation, selection []Obligation) (*stipulatorv1.InvocationHealth, []*stipulatorv1.TestResult, []*stipulatorv1.FailureDiagnostic, []*ProcessObservation, error) {
	return ExecuteInvocationObserved(ctx, n, selection, nil)
}

// ExecuteInvocationObserved is ExecuteInvocation with a per-package completion
// hook: onPackage fires, serialized, the moment a package's process has
// completed and been classified — while other packages still execute — so a
// caller can persist that package's evidence before the invocation ends
// (REQ-policy-cancellation-unit's unit of persistence). The hook runs after
// the package's spawn slot is released: a caller's publication holds no slot
// and spends none of the envelope a sibling still queued is waiting on — the
// record's envelope bounds processes alone (REQ-policy-explicit-time-bounds).
// The classification the hook sees is the one the invocation's report carries:
// the run is disposed once and the assembly reads the disposition. A hook
// error ends the invocation with it. A nil hook is ExecuteInvocation.
func ExecuteInvocationObserved(ctx context.Context, n *NormalizedInvocation, selection []Obligation, onPackage func(unit packageUnit) error) (*stipulatorv1.InvocationHealth, []*stipulatorv1.TestResult, []*stipulatorv1.FailureDiagnostic, []*ProcessObservation, error) {
	freezeWitnessAllocation(n, len(selectedPackages(selection)))
	legs := executionLegs(ctx, n, selectedPackages(selection))
	defer func() {
		for _, leg := range legs {
			leg.dispose()
		}
	}()
	return executeInvocationPrepared(ctx, n, selection, onPackage, legs)
}

func executeInvocationPrepared(ctx context.Context, n *NormalizedInvocation, selection []Obligation, onPackage func(unit packageUnit) error, proofs map[string]*packageLeg) (*stipulatorv1.InvocationHealth, []*stipulatorv1.TestResult, []*stipulatorv1.FailureDiagnostic, []*ProcessObservation, error) {
	pkgs := selectedPackages(selection)
	if len(pkgs) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("invocation %q: selection carries no package obligations", n.Name)
	}
	// The envelope carries its identity as the context cause: the kill path
	// dumps and graces only on true envelope expiry, never on a caller's
	// own deadline — a caller-bounded run is discarded whole, so a dump
	// there would have no consumer and the grace would only delay the
	// abort.
	invCtx, cancel := context.WithTimeoutCause(ctx, n.Timeout, errEnvelopeExpired)
	defer cancel()
	var (
		mu       sync.Mutex
		firstErr error
	)
	afterSlot := func(i int, run *packageRun) {
		mu.Lock()
		defer mu.Unlock()
		if err := finalizeRun(n, run, invCtx.Err() != nil, ""); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		if beforePackagePublishForTest != nil {
			beforePackagePublishForTest(run.pkg)
		}
		finishNativeRun(ctx, run)
		if firstErr != nil || onPackage == nil {
			return
		}
		if err := onPackage(packageUnit{pkg: pkgs[i], run: *run}); err != nil {
			firstErr = err
		}
	}
	runs := runSelectedPackages(ctx, invCtx, n, pkgs, nil, spawnOrdinals(), nil, afterSlot, proofs)
	if err := ctx.Err(); err != nil {
		// Caller cancellation: the partial run is discarded whole. The
		// envelope context is derived from ctx, so every child is already
		// terminated through its owned process boundary.
		return nil, nil, nil, nil, err
	}
	if firstErr != nil {
		return nil, nil, nil, nil, firstErr
	}
	return assembleInvocation(n, runs, invCtx.Err() != nil)
}

// spawnOrdinals issues process spawn ordinals, unique within one
// execution, so pid reuse never aliases two launched processes.
func spawnOrdinals() func() int32 {
	var (
		mu   sync.Mutex
		next int32
	)
	return func() int32 {
		mu.Lock()
		defer mu.Unlock()
		next++
		return next
	}
}

// runSelectedPackages fans the packages out under the derived
// concurrency bound, one owned process per package narrowed to its
// tests selection, with invCtx — the invocation envelope — governing
// every spawn. Runs the envelope denied before their spawn come back
// with no terminal disposition for the caller to classify. inSlot,
// when non-nil, runs for each package inside its own slot after its
// process — still holding the slot, so whatever it spawns (the
// selective form's isolation re-runs) counts against the bound as the
// package's own process tree, never beside it. afterSlot, when
// non-nil, runs for each package once its slot is released — the
// caller's completion work, which spawns nothing and so holds no slot:
// a sibling still queued on the bound is never delayed by it, and the
// envelope it is waiting on is spent on processes alone
// (REQ-policy-explicit-time-bounds). Both are skipped under the caller's
// cancellation.
func runSelectedPackages(ctx, invCtx context.Context, n *NormalizedInvocation, pkgs []string, tests TestSelection, spawnOrdinal func() int32, inSlot func(i int, run *packageRun, gate *admission), afterSlot func(i int, run *packageRun), proofs map[string]*packageLeg) []packageRun {
	gate := newAdmission(invCtx, spawnBoundOf(n))
	defer gate.leave()
	runs := make([]packageRun, len(pkgs))
	dispatched := false
	defer func() {
		if dispatched || ctx.Err() != nil {
			return
		}
		for i := range runs {
			if inSlot != nil {
				inSlot(i, &runs[i], nil)
			}
			if afterSlot != nil {
				afterSlot(i, &runs[i])
			}
		}
	}()
	profile, profileErr := openRetainedCoverage(n)
	if profileErr != nil {
		for i, pkg := range pkgs {
			runs[i] = degradedRun(n.Name, pkg, "coverage output: "+profileErr.Error(), false)
		}
		return runs
	}
	defer profile.Close()
	builds := map[string]*retainedBuildResult{}
	var artifacts []*retainedArtifact
	flags, flagErr := retainedGOFLAGS(n)
	if flagErr != nil {
		for i, pkg := range pkgs {
			runs[i] = degradedRun(n.Name, pkg, flagErr.Error(), false)
		}
		return runs
	}
	// The public execution surface can be given package obligations without an
	// earlier discovery. Resolve execution coordinates for that case, without
	// turning this later listing into native preparation or a runnable inventory.
	buildInvocation := *n
	runtimeDirs := maps.Clone(n.PkgDirs)
	if runtimeDirs == nil {
		runtimeDirs = map[string]string{}
	}
	missingDirectory := false
	for _, pkg := range pkgs {
		missingDirectory = missingDirectory || runtimeDirs[pkg] == ""
	}
	if missingDirectory || flags.coverPkg != "" && len(n.selectedPackages) == 0 {
		query := *n
		query.Packages = pkgs
		query.Env = witnessEnvOf(n)
		facts, err := readPackageListing(invCtx, &query, engineRunner, "ImportPath,Name,Dir,TestGoFiles,XTestGoFiles,Error")
		if err == nil {
			buildInvocation.selectedPackages = facts
			for _, fact := range facts {
				if runtimeDirs[fact.ImportPath] == "" {
					runtimeDirs[fact.ImportPath] = fact.Dir
				}
			}
		}
		// Compilation still owns build-failure classification. If it succeeds
		// without a resolvable runtime directory, runtime preparation refuses.
	}
	if flags.coverPkg != "" {
		admitted, refusal, held := gate.admit()
		if !admitted {
			for i, pkg := range pkgs {
				runs[i] = packageRun{pkg: pkg, heldBy: held}
				if refusal != "" {
					runs[i] = degradedRun(n.Name, pkg, "memory: "+refusal, false)
				}
			}
			return runs
		}
		for _, pkg := range pkgs {
			if beforePackageBuildForTest != nil {
				beforePackageBuildForTest(pkg)
			}
			proofs[pkg].prepareBuild(invCtx)
		}
		scope := buildRetainedScope(invCtx, &buildInvocation, pkgs, retainedProcessHooks{
			Spawned: func(_ string, pid int) { gate.spawned("coverage compilation", pid) },
			Reaped: func(_ string, pid int, state *os.ProcessState) {
				gate.reapedState("coverage compilation", pid, state)
			},
		})
		gate.release()
		defer scope.Close()
		for _, pkg := range pkgs {
			b, ok := scope.Packages[pkg]
			if !ok {
				b = retainedBuildResult{Err: scope.Err, Stream: scope.Stream, Stderr: scope.Stderr}
				if b.Err == nil {
					b.Err = fmt.Errorf("coverage compilation returned no package %s", pkg)
				}
			}
			builds[pkg] = &b
			if b.Artifact != nil {
				artifacts = append(artifacts, b.Artifact)
			}
		}
	}
	rep := progress.FromContext(ctx)
	var pkgsDone atomic.Int32
	var wg sync.WaitGroup
	dispatched = true
	for i, pkg := range pkgs {
		wg.Add(1)
		go func(i int, pkg string) {
			defer wg.Done()
			defer func() {
				runs[i].attempt.release()
				if execution := runs[i].execution; builds[pkg] == nil && execution != nil && execution.built != nil && execution.built.Artifact != nil {
					_ = execution.built.Artifact.Close()
				}
			}()
			// Every package reports its completion exactly once, whichever
			// way it ends; the reporter bounds emission.
			defer func() { rep.Step(n.Name, pkgsDone.Add(1), int32(len(pkgs))) }()
			waitStart := time.Now()
			admitted, refusal, held := gate.admit()
			queueWait := time.Since(waitStart)
			origin := gate.originWords()
			switch {
			case admitted:
				execution := &packageExecution{built: builds[pkg], coverage: profile, artifacts: artifacts, directory: runtimeDirs[pkg]}
				runs[i] = runNativePackage(ctx, invCtx, n, pkg, tests[pkg], spawnOrdinal(), gate, proofs[pkg], execution)
				runs[i].queueWait = queueWait
				runs[i].admissionOrigin = origin
				if inSlot != nil && ctx.Err() == nil {
					inSlot(i, &runs[i], gate)
				}
				gate.release()
			case refusal != "":
				// The host cannot hold one package process beside the
				// pass and nothing of this invocation is running to free
				// memory: refused stated, never spawned into the host's
				// guard (the witness concurrency clause's memory term).
				runs[i] = degradedRun(n.Name, pkg, "memory: "+refusal, false)
				runs[i].queueWait = queueWait
				runs[i].admissionOrigin = "memory: " + refusal
				if inSlot != nil && ctx.Err() == nil {
					inSlot(i, &runs[i], nil)
				}
			default:
				// Never spawned: the caller classifies the missing terminal
				// disposition as timeout or discards on cancellation; a
				// package the memory term was holding names the term in
				// its timeout diagnostic.
				runs[i] = packageRun{pkg: pkg, heldBy: held, queueWait: queueWait, admissionOrigin: held}
				if inSlot != nil && ctx.Err() == nil {
					inSlot(i, &runs[i], nil)
				}
			}
			if afterSlot != nil && ctx.Err() == nil {
				afterSlot(i, &runs[i])
			}
		}(i, pkg)
	}
	wg.Wait()
	cancelled := ctx.Err() != nil || invCtx.Err() != nil
	for i := range runs {
		if runs[i].pkg == "" {
			runs[i].pkg = pkgs[i]
		}
		pt := runs[i].buildTimingDiagnostic(n.Name, cancelled)
		rep.RecordPackageTiming(pt)
	}
	return runs
}

// finalizeRun classifies a run that reached no terminal fact of its own:
// under envelope expiry it becomes a reported TIMEOUT disposition — the
// cut-off process's retained output is part of the verdict
// (REQ-check-diagnostics), never discarded with the run, and a launched
// process that died before its testlog flushed still owns its
// observation, incomplete rather than silently absent. Outside expiry a
// missing terminal disposition has no in-spec cause and is surfaced as an
// error. soloTest names the single isolated runnable a solo process ran,
// so a denied re-run's timeout diagnostic names the test it denied.
func finalizeRun(n *NormalizedInvocation, r *packageRun, timedOut bool, soloTest string) error {
	if r.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_UNSPECIFIED {
		r.buildTimingDiagnostic(n.Name, false)
		return nil
	}
	// No terminal disposition of its own: the envelope deadline is the
	// only in-spec way to get here without caller cancellation.
	if !timedOut {
		return fmt.Errorf("invocation %q: package %s ended without a terminal disposition outside timeout and cancellation", n.Name, r.pkg)
	}
	r.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetInvocation(n.Name)
	d.SetPackage(r.pkg)
	if soloTest != "" {
		d.SetTest(soloTest)
	}
	d.SetDisposition(r.disposition)
	var out boundedBuffer
	out.write(fmt.Sprintf("invocation timeout %v expired before the package completed", n.Timeout))
	if r.heldBy != "" {
		out.write("\n" + heldByPrefix)
		out.write(r.heldBy)
	}
	if len(r.aborted) > 0 {
		line, rosterTruncated := formatBoundedRoster("\nstarted but unfinished: ", r.aborted)
		out.write(line)
		out.truncated = out.truncated || rosterTruncated
	}
	if r.residue != nil && !r.residue.empty() {
		out.write("\n")
		out.write(r.residue.b.String())
		out.truncated = out.truncated || r.residue.truncated
	}
	d.SetOutput(out.b.String())
	d.SetTruncated(out.truncated)
	r.diags = append(r.diags, d)
	if r.producer != nil {
		r.obs = incompleteObservation(r.pkg, r.producer,
			fmt.Sprintf("invocation timeout %v expired before the process completed", n.Timeout))
	}
	r.buildTimingDiagnostic(n.Name, true)
	return nil
}

// assembleInvocation turns the terminal runs into the invocation report:
// per-package health, attributed outcomes, diagnostics, and observations,
// with the invocation disposed as its worst package. It folds each
// finalized packageRun through SelectionResult.absorb so the two shapes
// share one folding path.
func assembleInvocation(n *NormalizedInvocation, runs []packageRun, timedOut bool) (*stipulatorv1.InvocationHealth, []*stipulatorv1.TestResult, []*stipulatorv1.FailureDiagnostic, []*ProcessObservation, error) {
	health := &stipulatorv1.InvocationHealth{}
	health.SetInvocation(n.Name)
	health.SetGo(resolvedConfig(n))
	folded := &SelectionResult{}
	for i := range runs {
		r := &runs[i]
		if err := finalizeRun(n, r, timedOut, ""); err != nil {
			return nil, nil, nil, nil, err
		}
		folded.absorb(packageUnit{pkg: r.pkg, run: *r})
	}
	packages := make([]*stipulatorv1.PackageHealth, 0, len(folded.Processes))
	invDisposition := stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY
	for _, p := range folded.Processes {
		if p.Test != "" {
			continue
		}
		ph := &stipulatorv1.PackageHealth{}
		ph.SetPackage(p.Package)
		ph.SetDisposition(p.Disposition)
		packages = append(packages, ph)
		invDisposition = worseDisposition(invDisposition, p.Disposition)
	}
	health.SetDisposition(invDisposition)
	health.SetPackages(packages)
	return health, folded.Tests, folded.Diagnostics, folded.Observations, nil
}

// worseDisposition aggregates package dispositions into the invocation's:
// healthy only when every package is, otherwise the most report-shaping
// failure wins — timeout over degradation over build failure over test
// failure — so the invocation names the reason its report cannot be
// trusted further.
func worseDisposition(a, b stipulatorv1.HealthDisposition) stipulatorv1.HealthDisposition {
	rank := func(d stipulatorv1.HealthDisposition) int {
		switch d {
		case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT:
			return 4
		case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED:
			return 3
		case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED:
			return 2
		case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED:
			return 1
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// selectedPackages extracts the sorted package obligations of a selection.
func selectedPackages(selection []Obligation) []string {
	seen := map[string]bool{}
	var pkgs []string
	for _, o := range selection {
		if o.Kind == ObligationPackage && !seen[o.Package] {
			seen[o.Package] = true
			pkgs = append(pkgs, o.Package)
		}
	}
	sort.Strings(pkgs)
	return pkgs
}

// packageEstimateFloor is the least memory the admission assumes one
// package process needs before any process of the invocation has shown
// its peak — a test binary's build and link step legitimately reach it
// (gomutant's oracle ceiling takes the same floor).
const packageEstimateFloor = uint64(1) << 30

// readingsHook is the admission's reading of the host and of the pass —
// resident.Readings in production; a test injects a host that cannot
// hold a process. reapedPeakHook observes each reap the gate receives —
// every process a package's slot spawned, the isolation re-runs
// included — with whether the gate had the process registered and the
// completed peak it folded: a test seam pinning the wiring of the
// completed-package evidence and of the tree attribution.
var (
	readingsHook   = resident.Readings
	reapedPeakHook func(pkg string, pid int, registered bool, peakBytes uint64)
)

// admission gates the spawn of package processes under the derived
// concurrency bound and, where the host reports its memory, the memory
// term: a package process is admitted while fewer than the bound run and
// the host's available memory, less what the running packages are
// estimated still to take, covers one more package at the invocation's
// estimate with the pass's own room to grow back to its peak left
// over. The measure is the host's (gofresh's readings carry it); the
// family's soft ceilings are collection targets, not needs — the pass's
// need beyond what it holds is its growth, below — so no half of the
// room is set aside for a pass that holds a fraction of it. Both terms
// read this admission's own observations, never the kernel's lifetime
// marks: the pass's peak is the largest set this invocation's
// admission has read at its asks (discovery's peak, over before the
// admission existed, would reserve memory nothing running takes), and
// the estimate is the largest a package's process tree has been seen
// to need — a completed package's largest process from its wait
// status and the largest a registered package's tree has shown in the
// readings — floored at packageEstimateFloor, so it grows as the
// packages' processes do and never shrinks within the invocation, and
// a descendant outside every registered tree (a resolver child leaving
// the table, a driver of the pass's own) prices no package; each
// running package is reserved its estimate less what ITS tree already
// shows in the process table (its held pages are out of the room
// already, its file-backed pages the page cache's, reclaimable and
// counted available; a tree attributed to the process the executor
// spawned for it; a tree not yet in the table reserves the whole
// estimate), so a burst of asks between the kernel's readings is
// bounded by the term and not only by the processor bound, and one
// tree's overshoot never pays for a sibling's reservation. Every
// process a package's slot spawns — its whole-package process and each
// isolation re-run — is registered at its spawn and reaped with its
// peak the moment its wait returns, so a running package's reservation
// reads whichever of its processes is live and a reaped process's peak
// prices the next spawn at once; between a reap and the slot's next
// spawn the package holds no registered tree and reserves the whole
// estimate — brief, stated. The estimate names its origin wherever the
// term's words appear — the floor, the package whose completed
// process's peak it is, or the package whose live tree showed it — so
// a genuine need and a transient read apart in the refusal, the held
// package's timeout diagnostic and the witnesses' bounded cause
// (REQ-evidence-admission-origin).
// A waiting package re-asks at every completion of the process (the
// readings move) and gives up with the invocation's context, carrying
// the words of the term that held it; a package asked while nothing of
// the process runs and the host cannot hold one process is refused —
// the refusal's words name the readings — rather than waiting on a
// completion that cannot come or spawning into the host's guard; the
// concurrent operations of one process are judged together on the one
// host gate (hostGate), each running package at its own invocation's
// estimate. The term only narrows:
// the processor bound and the inner width the witness environment
// delivers are never widened by it.
type admission struct {
	ctx context.Context
	// gate is the process's one host gate: the mutex and condition
	// every admission of the process shares, and the membership the
	// room is judged over.
	gate    *hostGate
	bound   int
	running int
	// pids are the processes spawned for the running packages — the
	// roots of the trees the reservation attributes.
	pids map[int]bool
	// peak is the largest completed-package peak seen so far;
	// peakOrigin names the package and process that showed it.
	peak       uint64
	peakOrigin estimateOrigin
	// pkgOf names the package each registered process was spawned for.
	pkgOf map[int]string
	// passPeak is the largest resident set the pass has shown in this
	// admission's readings — execution's own peak, the growth term's
	// reference.
	passPeak uint64
	// treePeak is the largest observed live-tree reading per registered
	// process. Compilation and execution roots occupy the slot sequentially;
	// a reaped root's live readings are replaced by its completed peak.
	treePeak map[int]uint64
}

// estimateOrigin names where the estimate's bytes come from: the
// floor, a package's completed process (its reaped peak), or a
// package's live tree (its largest reading under this admission).
type estimateOrigin struct {
	term string
	pkg  string
	pid  int
}

const (
	originFloor     = "the floor"
	originCompleted = "completed process"
	originLiveTree  = "live tree"
)

// words renders the origin for the term's words.
func (o estimateOrigin) words() string {
	switch o.term {
	case originCompleted:
		return fmt.Sprintf("package %s's completed process %d's peak", o.pkg, o.pid)
	case originLiveTree:
		return fmt.Sprintf("package %s's live tree (process %d) in this invocation's readings", o.pkg, o.pid)
	default:
		return originFloor
	}
}

// hostGate is the process's one host gate: the one mutex and condition
// every admission of the process shares, and the set of admissions
// alive — concurrent operations' (a long-lived server's calls, each
// with its own capture, envelope and bound). Every judgment runs
// under the gate's lock over one reading and every member's running
// packages, so the room is never read from a published snapshot (a
// figure that could go stale between an operation's judgments), a
// waiter waits under the lock that guards what it judged (no wakeup
// is lost), every reap, release and end of any member wakes every
// waiter, and an admission captures the gate at its construction
// (REQ-evidence-admission-origin).
type hostGate struct {
	mu      sync.Mutex
	cond    *sync.Cond
	members map[*admission]bool
	// gen counts the wakes (every broadcast); reading is the one
	// reading the waiters woken by the latest wake judge over, taken
	// by the first of them — a fresh ask takes its own.
	gen        uint64
	readingGen uint64
	reading    resident.Reading
	readingOK  bool
}

func newHostGate() *hostGate {
	g := &hostGate{members: map[*admission]bool{}}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// wake broadcasts the gate's condition, opening a new wake generation:
// the woken waiters judge over one reading, taken once.
func (g *hostGate) wake() {
	g.gen++
	g.cond.Broadcast()
}

// readingFor answers the reading a judgment runs over, under g.mu: a
// fresh ask takes its own (the host moves without events); a waiter
// woken by the latest wake takes the generation's, read once for the
// whole burst, so a burst of W waiters costs one walk, not W.
func (g *hostGate) readingFor(fresh bool) (resident.Reading, bool) {
	if fresh || g.readingGen != g.gen {
		g.reading, g.readingOK = readingsHook()
		g.readingGen = g.gen
	}
	return g.reading, g.readingOK
}

// theHostGate is the process's gate; a unit pin swaps in a fresh one
// before its admissions are minted.
var theHostGate = newHostGate()

// join adds a under g.mu.
func (g *hostGate) join(a *admission) {
	g.mu.Lock()
	g.members[a] = true
	g.mu.Unlock()
}

// leave removes a — its invocation returned, nothing of it running —
// and wakes every waiter: the room its packages held is free.
func (g *hostGate) leave(a *admission) {
	g.mu.Lock()
	delete(g.members, a)
	g.wake()
	g.mu.Unlock()
}

// member reports whether a is alive on the gate.
func (g *hostGate) member(a *admission) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.members[a]
}

// admissionObserverForTest, when set, sees every admission minted —
// the seam a pin follows an invocation's gate through to its drop;
// nil in production.
var admissionObserverForTest func(*admission)

func newAdmission(ctx context.Context, bound int) *admission {
	a := &admission{ctx: ctx, gate: theHostGate, bound: bound, pids: map[int]bool{}, pkgOf: map[int]string{}, treePeak: map[int]uint64{}}
	if admissionObserverForTest != nil {
		admissionObserverForTest(a)
	}
	a.gate.join(a)
	// The context's end wakes every waiter, which then returns
	// unadmitted. Membership ends at the invocation's return alone: a
	// context ending on a timeout or a cancellation leaves the
	// invocation's packages running through their kill grace, and
	// their trees must be judged until they are reaped and released.
	context.AfterFunc(ctx, func() {
		a.gate.mu.Lock()
		a.gate.wake()
		a.gate.mu.Unlock()
	})
	return a
}

// estimate is the memory one more package process tree is assumed to
// need, given the reading's trees of the registered package processes
// — the observed maxima advance under the gate's lock with the reading
// — and the
// origin of that figure.
func (a *admission) estimate(reading resident.Reading) (uint64, estimateOrigin) {
	need, origin := packageEstimateFloor, estimateOrigin{term: originFloor}
	if a.peak > need {
		need, origin = a.peak, a.peakOrigin
	}
	for _, pid := range slices.Sorted(maps.Keys(a.pids)) {
		if shown := reading.Trees[pid]; shown > a.treePeak[pid] {
			a.treePeak[pid] = shown
		}
		if a.treePeak[pid] > need {
			need, origin = a.treePeak[pid], estimateOrigin{term: originLiveTree, pkg: a.pkgOf[pid], pid: pid}
		}
	}
	return need, origin
}

// room judges the memory term under the gate's lock: whether the host
// can hold one
// more package process beside the pass and the packages already
// running, and the readings' words when it cannot. A host or a pass
// without a reading has no memory term.
func (a *admission) room(fresh bool) (ok bool, words string, othersRunning bool) {
	reading, ok := a.gate.readingFor(fresh)
	if !ok {
		return true, "", false
	}
	set := reading.Set
	if set.ProcessBytes > a.passPeak {
		a.passPeak = set.ProcessBytes
	}
	need, origin := a.estimate(reading)
	// Each running package's tree is reserved the estimate less what it
	// already shows in the process table (its held pages are out of the
	// room already, its file-backed pages reclaimable); a package
	// admitted but not yet registered, or registered but not yet in the
	// table, reserves the whole estimate. Per tree, never netted across
	// trees: a grown sibling's bytes pay for nothing but itself.
	reserved := a.reservation(reading, need)
	// The other admissions of this process — concurrent operations'
	// — hold their running packages against the same available memory,
	// each at its own estimate over this same reading.
	var others, othersShown uint64
	for b := range a.gate.members {
		if b == a {
			continue
		}
		if b.running > 0 {
			othersRunning = true
		}
		needB, _ := b.estimate(reading)
		others += b.reservation(reading, needB)
		for pid := range b.pids {
			othersShown += reading.Trees[pid]
		}
	}
	// The pass's own room to grow back to the largest set any
	// admission of the process has read: the process grows once, and a
	// later operation's admission must not admit into the headroom an
	// earlier one's pass needs.
	passPeak := a.passPeak
	for b := range a.gate.members {
		passPeak = max(passPeak, b.passPeak)
	}
	growth := passPeak - set.ProcessBytes
	// The room is what the host has available: the pass's and the
	// children's held pages are out of it already, and the family's
	// soft ceilings are targets, not needs.
	available := reading.Host.AvailableBytes
	if available >= others+reserved+need && available-others-reserved-need >= growth {
		return true, "", othersRunning
	}
	// The estimate and its origin lead the words: the witnesses' cause
	// carries the line bounded (packageReasonBound), and the deciding
	// part must survive the cut — the readings follow.
	words = fmt.Sprintf(termLead+"%s — %s; the host cannot hold one more package process beside the pass: available %s, %d package(s) running reserving %s, the pass's resident %s (this phase's peak %s)",
		resident.ByteWord(need), origin.words(), resident.ByteWord(available), a.running, resident.ByteWord(reserved), resident.ByteWord(set.ProcessBytes), resident.ByteWord(passPeak))
	if othersRunning {
		words += fmt.Sprintf(", this process's other operations running %d package(s) reserving %s beyond the %s their trees show", a.gate.runningOthers(a), resident.ByteWord(others), resident.ByteWord(othersShown))
	}
	return false, words, othersRunning
}

// reservation is what the admission's running packages still reserve
// beyond what the reading's table shows of their trees, at need.
func (a *admission) reservation(reading resident.Reading, need uint64) uint64 {
	registered := 0
	reserved := uint64(0)
	for pid := range a.pids {
		registered++
		if shown := reading.Trees[pid]; shown < need {
			reserved += need - shown
		}
	}
	if a.running > registered {
		reserved += uint64(a.running-registered) * need
	}
	return reserved
}

// runningOthers counts the other members' running packages, under g.mu.
func (g *hostGate) runningOthers(a *admission) int {
	n := 0
	for b := range g.members {
		if b != a {
			n += b.running
		}
	}
	return n
}

// admit blocks until the package may spawn. admitted is false when the
// invocation's context ended — held then carries the words of the memory
// term that was holding the package, empty when it waited on the
// processor bound alone — or when nothing of the process runs and the
// host cannot hold one process (refusal names the readings).
func (a *admission) admit() (admitted bool, refusal, held string) {
	g := a.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	fresh := true
	for {
		if a.ctx.Err() != nil {
			return false, "", held
		}
		held = ""
		if a.running < a.bound {
			ok, words, othersRunning := a.room(fresh)
			if ok {
				a.running++
				return true, "", ""
			}
			// Nothing of this invocation running and no other operation
			// of the process running a package: no completion can come,
			// so the package is refused rather than held; another
			// operation's package completes in time, and its reap,
			// release or end wakes this waiter.
			if a.running == 0 && !othersRunning {
				return false, words, ""
			}
			held = words
		}
		g.cond.Wait()
		fresh = false
	}
}

// spawned registers a process an admitted package's executor spawned
// for pkg — the whole-package process or an isolation re-run — the
// root of the tree the reservation attributes to the package.
func (a *admission) spawned(pkg string, pid int) {
	if a == nil {
		return
	}
	a.gate.mu.Lock()
	a.pids[pid] = true
	a.pkgOf[pid] = pkg
	a.gate.mu.Unlock()
}

// childProcessPeakBytes derives a reaped child process's post-exec peak from
// its wait-status maxrss, the parent process's high-water mark, and the live
// process-tree peak observed while the child was registered. On Linux, Go's
// process-group spawn clones with CLONE_VM | CLONE_VFORK and exec_mmap seeds
// the child's signal->maxrss with get_mm_hiwater_rss(old_mm), clamping the
// child's wait-status Maxrss from below by the parent's pre-exec high-water
// mark (RUSAGE_SELF.Maxrss). When parentPeak exceeds packageEstimateFloor and
// rawPeak does not exceed parentPeak, rawPeak reflects the inherited parent
// high-water ceiling rather than a post-exec child peak above the floor, so
// the child's post-exec evidence is bounded by its observed live-tree peak and
// the sub-floor wait-status mark; any rawPeak strictly above parentPeak is
// genuine post-exec child or descendant memory.
func childProcessPeakBytes(rawPeak, parentPeak, liveTreePeak uint64) uint64 {
	if parentPeak > packageEstimateFloor && rawPeak <= parentPeak {
		return max(liveTreePeak, min(rawPeak, packageEstimateFloor))
	}
	return max(rawPeak, liveTreePeak)
}

// reapedState unregisters a package's reaped OS process and folds its
// post-exec completed peak into the estimate after discounting any pre-exec
// parent high-water mark inherited across CLONE_VM | CLONE_VFORK.
func (a *admission) reapedState(pkg string, pid int, state *os.ProcessState) {
	if a == nil {
		return
	}
	rawPeak := processPeakBytes(state)
	parentPeak := parentPeakBytes()
	a.gate.mu.Lock()
	liveTreePeak := a.treePeak[pid]
	a.gate.mu.Unlock()
	a.reaped(pkg, pid, childProcessPeakBytes(rawPeak, parentPeak, liveTreePeak))
}

// reaped unregisters a package's process the moment its wait returned
// and folds its completed peak into the estimate, naming the origin,
// then wakes every waiter of the process to re-ask (the readings
// move); the seam sees what the gate received. The package's slot
// stays held until release.
func (a *admission) reaped(pkg string, pid int, peakBytes uint64) {
	if a == nil {
		return
	}
	a.gate.mu.Lock()
	registered := a.pids[pid]
	delete(a.pids, pid)
	delete(a.treePeak, pid)
	delete(a.pkgOf, pid)
	if peakBytes > a.peak {
		a.peak, a.peakOrigin = peakBytes, estimateOrigin{term: originCompleted, pkg: pkg, pid: pid}
	}
	a.gate.wake()
	a.gate.mu.Unlock()
	if reapedPeakHook != nil {
		reapedPeakHook(pkg, pid, registered, peakBytes)
	}
}

// release returns an admitted package's slot — its processes already
// reaped — and wakes every waiter of the process to re-ask: a slot
// freed is room any operation may take.
func (a *admission) release() {
	a.gate.mu.Lock()
	a.running--
	a.gate.wake()
	a.gate.mu.Unlock()
}

// leave ends the admission's membership of the gate: its invocation
// returned.
func (a *admission) leave() { a.gate.leave(a) }

func (a *admission) originWords() string {
	if a == nil {
		return ""
	}
	a.gate.mu.Lock()
	defer a.gate.mu.Unlock()
	if a.peak > packageEstimateFloor {
		return a.peakOrigin.words()
	}
	return originFloor
}

// witnessSpawnBound shares the parent's processor budget over the permitted
// package population. Each package is a parallel process tree; reserving slots
// for packages that cannot execute would unnecessarily narrow every child.
func witnessSpawnBound(processors, packages int) int {
	return min(max(1, processors/2), max(1, packages))
}

// spawnBoundOf is every post-normalize consumer's road to the fan-out
// bound: the pre-capture freeze (n.SpawnBound), so the spawn and
// every later report of it observe one value — a re-derivation at use
// time would race dynamic GOMAXPROCS updates exactly as the WitnessEnv
// field doc describes for the inner width. The fallback re-derivation
// exists only for hand-built invocations in tests.
func spawnBoundOf(n *NormalizedInvocation) int {
	if n.SpawnBound > 0 {
		return n.SpawnBound
	}
	return max(1, processorBudgetOf(n)/2)
}

// witnessChildWidth derives one unit's inner-parallelism width: the
// parent's processor budget over the unit bound, floored at one, so
// units x per-unit width stays at most the processor count - without
// it each unit is a full-width process tree and the fan-out multiplies
// into units x procs runnable threads, the load the unit bound alone
// never limited. Deriving from the parent's own GOMAXPROCS budget
// (not the raw core count) honors an operator who already narrowed
// the stipulator process.
func witnessChildWidth(n *NormalizedInvocation) int {
	width := processorBudgetOf(n) / spawnBoundOf(n)
	if width < 1 {
		width = 1
	}
	return width
}

// beforePackageBuildForTest places an edit immediately before the producer's
// build-input validation and compiler spawn.
var beforePackageBuildForTest func(pkg string)

// declaredBinaryBound extracts the binary deadline the reviewed record
// declares — the value of the last -test.timeout token in the reviewed args,
// matching the test binary's own last-one-wins parse — and "" when the record
// declares none. The reviewed record is the only source of the bound the
// classifier may name (REQ-policy-explicit-time-bounds); the panic shape in
// the child's output is detection, never the value.
func declaredBinaryBound(n *NormalizedInvocation) string {
	bound := ""
	for i := 0; i < len(n.Args); i++ {
		if !strings.HasPrefix(n.Args[i], "-") {
			continue
		}
		arg := strings.TrimPrefix(strings.TrimPrefix(n.Args[i], "-"), "-")
		switch {
		case strings.HasPrefix(arg, "test.timeout="):
			bound = strings.TrimPrefix(arg, "test.timeout=")
		case arg == "test.timeout" && i+1 < len(n.Args):
			i++
			bound = n.Args[i]
		}
	}
	// Zero and negative spellings declare no bound at all — the testing
	// runtime disables its alarm for them, so no deadline panic can
	// exist under such a record — and a value that is not a duration
	// declares nothing either.
	if d, err := time.ParseDuration(bound); err != nil || d <= 0 {
		return ""
	}
	return bound
}

// treeRoot recovers the verification tree root from the normalized
// invocation's absolute module directory and tree-relative module root.
func treeRoot(n *NormalizedInvocation) string {
	if n.ModuleRoot == "" {
		return n.Dir
	}
	return strings.TrimSuffix(n.Dir, string(filepath.Separator)+filepath.FromSlash(n.ModuleRoot))
}

// streamState is the parsed form of one package's command stream.
type streamState struct {
	// terminal is the package-level terminal action: "pass", "fail",
	// "skip", or empty when the stream ended without one.
	terminal string
	// failedBuild reports a build-fail event or a terminal fail event
	// naming a failed build.
	failedBuild bool
	events      int
	// malformed retains the first unparseable bytes, when any; the
	// diagnostic renders a bounded share of it (remainderShare) and
	// marks the cut.
	malformed string
	// postTerminal reports events after the terminal package event — a
	// shape the toolchain never produces, refused rather than trusted.
	postTerminal bool
	// sawAbort reports abort output (a panic, a runtime fatal) anywhere in
	// the stream: the testlog flush of such a process cannot be trusted.
	sawAbort bool
	// binaryTimeout records that the test binary's own deadline panic
	// ("panic: test timed out after <dur>") appeared, carrying the
	// panic's printed duration. Detection only: the classifier
	// reclassifies solely under a reviewed declared bound, and the
	// diagnostic names that reviewed bound, never this printed value.
	binaryTimeout string
	// roster collects the deadline panic's own "running tests:" entries
	// — the testing runtime's account of the subjects the deadline cut
	// off. Event ordering cannot identify them: a completed failure's
	// fail event may flush after the panic line, so the runtime's roster
	// is the only trustworthy victim list.
	roster []string
	// rosterOpen tracks being inside the dump's roster block.
	rosterOpen bool
	// pkgOutput is package-level output: build diagnostics and package
	// FAIL/ok lines.
	pkgOutput boundedBuffer
	// perTest accumulates each named test's own output until its terminal
	// event.
	perTest map[string]*boundedBuffer
	// started tracks tests that began and have not reached a terminal
	// event — abort residue when the package dies under them.
	started map[string]bool
	// startOrder preserves first-appearance order for deterministic
	// residue rendering.
	startOrder []string
	// regs accumulates each named test's runtime registrations until its
	// terminal event.
	regs        map[string][]string
	startTimes  map[string]time.Time
	testTimings []*stipulatorv1.TestTimingDiagnostic
	tests       []*stipulatorv1.TestResult
	diags       []*stipulatorv1.FailureDiagnostic
}

// collectTestTimings returns the completed per-test timings plus an entry
// marked running_at_abort for each test that started without reaching a
// terminal event.
func (st *streamState) collectTestTimings(abortedAt time.Time) []*stipulatorv1.TestTimingDiagnostic {
	if st == nil {
		return nil
	}
	out := slices.Clone(st.testTimings)
	for _, name := range startedTests(st) {
		td := &stipulatorv1.TestTimingDiagnostic{}
		td.SetTest(name)
		td.SetOutcome(stipulatorv1.TestOutcome_TEST_OUTCOME_NOT_RUN)
		td.SetRunningAtAbort(true)
		if start := st.startTimes[name]; !start.IsZero() && abortedAt.After(start) {
			td.SetElapsed(durationpb.New(abortedAt.Sub(start)))
		} else {
			td.SetElapsed(durationpb.New(0))
		}
		out = append(out, td)
	}
	return out
}

// parseTestStream consumes Go's test/build JSON stream, recording named
// test outcomes attributed to producer — subtests under the same producer
// as their parent, in stream order — with each occurrence's runtime
// registrations, and retaining bounded failure output. It never
// classifies health — classification needs the process exit and context
// state the caller holds.
func parseTestStream(invocation, pkg string, r io.Reader, producer *stipulatorv1.ProducerIdentity) *streamState {
	st := &streamState{
		perTest:    map[string]*boundedBuffer{},
		started:    map[string]bool{},
		regs:       map[string][]string{},
		startTimes: map[string]time.Time{},
	}
	dec := json.NewDecoder(r)
	for {
		var e testEvent
		if err := dec.Decode(&e); err != nil {
			if err == io.EOF {
				break
			}
			// An unparseable line poisons the stream: retain what remains
			// for the diagnostic and stop trusting anything after it.
			var rest boundedBuffer
			rest.write(err.Error())
			rest.write("; unparsed remainder: ")
			buf := make([]byte, failureOutputCap)
			m, _ := io.ReadFull(io.MultiReader(dec.Buffered(), r), buf)
			rest.write(string(buf[:m]))
			// Drain so the child never blocks on a full pipe.
			_, _ = io.Copy(io.Discard, r)
			st.malformed = rest.b.String()
			return st
		}
		st.events++
		if st.terminal != "" {
			// The terminal package event ends a well-formed stream; the
			// classifier refuses anything that follows it.
			st.postTerminal = true
		}
		if e.Action == "output" {
			if isAbortOutput(e.Output) {
				st.sawAbort = true
			}
			st.scanTimeoutDump(e.Output)
		}
		switch e.Action {
		case "build-output":
			st.pkgOutput.write(e.Output)
			continue
		case "build-fail":
			st.failedBuild = true
			continue
		}
		if e.Test == "" {
			switch e.Action {
			case "output":
				st.pkgOutput.write(e.Output)
			case "pass", "fail", "skip":
				st.terminal = e.Action
				if e.FailedBuild != "" {
					st.failedBuild = true
				}
			}
			continue
		}
		switch e.Action {
		case "run":
			if !st.started[e.Test] && st.perTest[e.Test] == nil {
				st.startOrder = append(st.startOrder, e.Test)
			}
			st.started[e.Test] = true
			if !e.Time.IsZero() && st.startTimes[e.Test].IsZero() {
				st.startTimes[e.Test] = e.Time
			}
		case "output":
			bb := st.perTest[e.Test]
			if bb == nil {
				bb = &boundedBuffer{}
				st.perTest[e.Test] = bb
				if !st.started[e.Test] {
					st.startOrder = append(st.startOrder, e.Test)
				}
			}
			bb.write(e.Output)
			// Runtime registrations attribute to the exact test whose
			// output carried them — subtest-granular by construction.
			for _, m := range coversRe.FindAllStringSubmatch(e.Output, -1) {
				st.regs[e.Test] = append(st.regs[e.Test], m[1])
			}
		case "pass", "fail", "skip":
			delete(st.started, e.Test)
			tr := &stipulatorv1.TestResult{}
			tr.SetPackage(pkg)
			tr.SetTest(e.Test)
			tr.SetOutcome(outcomeOf(e.Action))
			tr.SetProducer(producer)
			if regs := st.regs[e.Test]; len(regs) > 0 {
				sort.Strings(regs)
				tr.SetRegistrations(slices.Compact(regs))
				delete(st.regs, e.Test)
			}
			st.tests = append(st.tests, tr)
			td := &stipulatorv1.TestTimingDiagnostic{}
			td.SetTest(e.Test)
			td.SetOutcome(outcomeOf(e.Action))
			dur := time.Duration(e.Elapsed * float64(time.Second))
			if dur == 0 {
				if start := st.startTimes[e.Test]; !start.IsZero() && !e.Time.IsZero() && e.Time.After(start) {
					dur = e.Time.Sub(start)
				}
			}
			td.SetElapsed(durationpb.New(dur))
			st.testTimings = append(st.testTimings, td)
			delete(st.startTimes, e.Test)
			if e.Action == "fail" {
				d := &stipulatorv1.FailureDiagnostic{}
				d.SetInvocation(invocation)
				d.SetPackage(pkg)
				d.SetTest(e.Test)
				d.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
				if bb := st.perTest[e.Test]; bb != nil {
					d.SetOutput(bb.b.String())
					d.SetTruncated(bb.truncated)
				}
				st.diags = append(st.diags, d)
			}
			delete(st.perTest, e.Test)
		}
	}
	return st
}

// scanTimeoutDump feeds one output event's lines to the deadline-panic
// recognizer: the panic line itself, then the dump's "running tests:"
// roster, closed by the dump's goroutine section header. Line-oriented
// so the dump parses identically whether the toolchain delivers it as
// one event or one event per line.
func (st *streamState) scanTimeoutDump(s string) {
	for line := range strings.Lines(s) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case st.binaryTimeout == "":
			if strings.HasPrefix(line, "panic: test timed out") {
				if m := binaryTimeoutRe.FindStringSubmatch(line); m != nil {
					st.binaryTimeout = m[1]
				}
			}
		case line == "\trunning tests:":
			st.rosterOpen = true
		case st.rosterOpen:
			if m := timeoutRosterRe.FindStringSubmatch(line); m != nil {
				st.roster = append(st.roster, m[1])
			} else if strings.HasPrefix(line, "goroutine ") {
				// The dump's goroutine section header ends the roster.
				// Goroutines still running while the dump prints can
				// interleave their own writes — a bare newline included —
				// between entries; skipping everything else instead of
				// closing keeps the roster complete, and nothing after a
				// dump can match the entry shape (frames are single-tab
				// with no space before the parenthesis), so a roster the
				// header never closes collects nothing further either.
				st.rosterOpen = false
			}
		}
	}
}

// runResidue renders what a run left behind, in one order for every
// diagnostic that carries it — the caller's head (a degrade reason, a
// deadline sentence, none), then the unparsed stream remainder when
// the stream was poisoned (the refusal's own evidence, ahead of
// anything the cap could spend on: the classifier refuses a poisoned
// stream before its terminal ladder, so only the degrade and cut-off
// diagnostics carry it), the package-level output, each
// started-but-unfinished test's name with its buffered output where
// it left any (a test with no terminal event died with the package,
// and its output is the failure's residue under its name; on the
// deadline arm the head's roster is what attributes the denied
// subjects — an unfinished test the runtime did not list is sectioned
// for its output alone), the child's stderr (where an
// envelope kill's goroutine dump lands), and the process exit —
// truncation propagated from every buffer rendered. The remainder is
// rendered as a bounded share (remainderShare) with its cut marked: a
// poison is identified by its prefix, and a cap-sized remainder must
// not displace the package output — a poisoned build stream's
// compiler diagnostic — that the rest of the cap retains. The cut-off,
// degrade, and terminal-fail diagnostics are this one composition
// with their own head.
func runResidue(st *streamState, stderr *boundedBuffer, waitErr error, head string) *boundedBuffer {
	var out boundedBuffer
	section := func(s string) {
		if !out.empty() {
			out.write("\n")
		}
		out.write(s)
	}
	if head != "" {
		section(head)
	}
	if st.malformed != "" {
		section("malformed stream: ")
		// Raw stream bytes: scrubbed to valid text, since the
		// diagnostic rides a UTF-8-validated proto field whose marshal
		// refuses the whole report otherwise (the JSON-decoded sections
		// are valid by decoding; this one and stderr are not).
		scrubbed := strings.ToValidUTF8(st.malformed, "\uFFFD")
		remainder := cutAtRune(scrubbed, remainderShare)
		if len(remainder) < len(scrubbed) {
			// The mark compares the cut against the text it cut: the
			// scrub can widen a byte into a three-byte rune, so the raw
			// length is no measure of it.
			out.truncated = true
		}
		out.write(remainder)
	}
	if !st.pkgOutput.empty() {
		section("package output:\n")
		out.write(st.pkgOutput.b.String())
		out.truncated = out.truncated || st.pkgOutput.truncated
	}
	for _, name := range st.startOrder {
		bb := st.perTest[name]
		if !st.started[name] || bb == nil || bb.empty() {
			continue
		}
		section(fmt.Sprintf("--- aborted: %s ---\n", name))
		out.write(bb.b.String())
		out.truncated = out.truncated || bb.truncated
	}
	if !stderr.empty() {
		section("stderr:\n")
		out.write(strings.ToValidUTF8(stderr.b.String(), "�"))
		out.truncated = out.truncated || stderr.truncated
	}
	if waitErr != nil {
		section(fmt.Sprintf("process exit: %v", waitErr))
	}
	return &out
}

// startedTests returns, in first-appearance order, the tests a cut-off
// stream had started without finishing.
func startedTests(st *streamState) []string {
	var names []string
	for _, name := range st.startOrder {
		if st.started[name] {
			names = append(names, name)
		}
	}
	return names
}

func outcomeOf(action string) stipulatorv1.TestOutcome {
	switch action {
	case "pass":
		return stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED
	case "fail":
		return stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED
	}
	return stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED
}

// classifyRun turns a parsed stream plus its process exit into the
// package's terminal disposition. The refusal ladder comes first: a
// malformed stream, a stream without a terminal package/build event, or a
// process that produced no events at all — exit status notwithstanding —
// is degraded, never healthy, because a report that cannot prove the suite
// ran cannot certify it passed.
func classifyRun(invocation, pkg string, st *streamState, waitErr error, stderr *boundedBuffer, binaryBound string) packageRun {
	run := packageRun{pkg: pkg, tests: st.tests, diags: st.diags}
	degrade := func(reason string) packageRun {
		out := runResidue(st, stderr, waitErr, reason)
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED
		d := &stipulatorv1.FailureDiagnostic{}
		d.SetInvocation(invocation)
		d.SetPackage(pkg)
		d.SetDisposition(run.disposition)
		d.SetOutput(out.b.String())
		d.SetTruncated(out.truncated)
		run.diags = append(run.diags, d)
		return run
	}
	// `go test -c -json` can end at build-fail without emitting a test
	// package terminal. That compiler fact plus an ordinary nonzero exit
	// establishes build failure; a missing artifact, failed start, or signal
	// does not. Ownership and conversion faults poison their stream before
	// reaching this shared classifier.
	var exit *exec.ExitError
	failedCompilation := st.failedBuild && errors.As(waitErr, &exit) && exit.ExitCode() > 0
	switch {
	case st.malformed != "":
		return degrade("go test -json stream carried unparseable output")
	case st.events == 0:
		return degrade("go test -json produced no events; a silent command stream is refused")
	case st.terminal == "" && !failedCompilation:
		return degrade("go test -json stream ended without a terminal package event")
	case st.postTerminal:
		return degrade("go test -json stream carried events after the terminal package event; a stream that outlives its own verdict is refused")
	}
	switch st.terminal {
	case "pass", "skip":
		if waitErr != nil {
			// A green stream from a red process is a contradiction the
			// report must not paper over.
			return degrade("go test exited with failure despite a passing stream")
		}
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY
		return run
	}
	// Terminal fail: a build failure when the toolchain says so; a binary
	// deadline when the testing runtime's own panic appeared AND the
	// reviewed record declares a binary bound — the reviewed bound is the
	// red fact the diagnostic names (REQ-policy-budget-attribution), and
	// detection alone never reclassifies a run whose record declares no
	// deadline; otherwise suite semantics — assertion failures, panics,
	// red TestMain — exactly the failure classes a direct `go test`
	// exits non-zero for. Completed outcomes, a genuine failure whose
	// fail event flushed after the panic line included, stand untouched
	// in every arm.
	deadline := st.binaryTimeout != "" && binaryBound != "" && !st.failedBuild
	switch {
	case st.failedBuild:
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED
	case deadline:
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT
	default:
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED
	}
	head := ""
	rosterTruncated := false
	if deadline {
		head = fmt.Sprintf("test binary timeout %s exhausted before the package completed — the budget is the red fact, not the running tests", binaryBound)
		// The runtime's own roster is the victim list; event ordering is
		// not (a completed failure can flush after the panic line). The
		// started set stands in only when the dump carried no roster.
		names := st.roster
		if len(names) == 0 {
			names = startedTests(st)
		}
		if len(names) > 0 {
			var line string
			line, rosterTruncated = formatBoundedRoster("\nrunning when the budget expired: ", names)
			head += line
		}
	}
	out := runResidue(st, stderr, waitErr, head)
	out.truncated = out.truncated || rosterTruncated
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetInvocation(invocation)
	d.SetPackage(pkg)
	d.SetDisposition(run.disposition)
	d.SetOutput(out.b.String())
	d.SetTruncated(out.truncated)
	run.diags = append(run.diags, d)
	return run
}

// heldByPrefix opens the timeout diagnostic's line naming the memory
// term that held a package until the envelope expired.
const heldByPrefix = "held by the memory term: "

// packageReasonBound bounds the reason's line in a no-outcome cause.
const packageReasonBound = 200

// packageReason is the host's part of a package's no-outcome cause,
// read from the package-scoped diagnostic that carries the package's
// own disposition under the invocation — the degradation's own, which
// the classifier appends after any stream diagnostics: for a degraded
// package (a memory refusal, a toolchain or telemetry degradation, a
// stream the classifier refused) that diagnostic's first line; for a
// timed-out package the memory term's line when the term held it,
// nothing otherwise (a timeout or a test failure is the package's own
// outcome, its cause the disposition alone). The line is bounded;
// empty when the package carries no such diagnostic.
func packageReason(diags []*stipulatorv1.FailureDiagnostic, invocation, pkg string, disposition stipulatorv1.HealthDisposition) string {
	var own *stipulatorv1.FailureDiagnostic
	for _, d := range diags {
		if d.GetInvocation() == invocation && d.GetPackage() == pkg && d.GetTest() == "" && d.GetDisposition() == disposition {
			own = d
		}
	}
	if own == nil {
		return ""
	}
	switch disposition {
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED:
		line, _, _ := strings.Cut(own.GetOutput(), "\n")
		return boundedReason(line)
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT:
		// The term's line is the diagnostic's second line, where
		// finalizeRun writes it — never read from the cut-off process's
		// residue below, whose text is the test's own.
		if _, rest, ok := strings.Cut(own.GetOutput(), "\n"); ok {
			if line, _, _ := strings.Cut(rest, "\n"); strings.HasPrefix(line, heldByPrefix) {
				return boundedReason(line)
			}
		}
	}
	return ""
}

// boundedReason bounds a no-outcome cause's line. The memory term's
// line — opening with the refusal's or the held line's prefix and the
// term's first words — keeps its deciding part whole — the estimate
// and its origin, everything before the first "; " — and bounds the
// readings that follow (REQ-evidence-admission-origin); any other
// line is bounded whole.
func boundedReason(line string) string {
	if strings.HasPrefix(line, "memory: "+termLead) || strings.HasPrefix(line, heldByPrefix+termLead) {
		if head, rest, ok := strings.Cut(line, "; "); ok {
			return head + "; " + cutAtRune(rest, packageReasonBound)
		}
	}
	return cutAtRune(line, packageReasonBound)
}

// termLead opens the memory term's words: the estimate and its origin
// lead, the readings follow.
const termLead = "one package estimated at "

// degradedRun is a spawn-stage degradation: the package never produced a
// stream at all.
func degradedRun(invocation, pkg, reason string, truncated bool) packageRun {
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetInvocation(invocation)
	d.SetPackage(pkg)
	d.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED)
	d.SetOutput(reason)
	d.SetTruncated(truncated)
	return packageRun{
		pkg:         pkg,
		disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED,
		diags:       []*stipulatorv1.FailureDiagnostic{d},
	}
}

// writerFunc adapts a function to io.Writer (the bounded stderr sink, the
// engine diagnostics sink).
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// ExecutePolicy executes every Go invocation of the accepted policy
// exactly once against the tree at dir and assembles the execution
// report: per-invocation and per-package terminal health carrying each
// invocation's resolved configuration, attributed test outcomes, bounded
// failure diagnostics, per-process runtime observations, and the
// conservation findings of the policy against the workspace's
// default-selection obligation universe (REQ-policy-conservation). The
// returned observations are the report's own, with each completed
// record's live gofresh evidence beside its wire form for in-process
// consumers — gofresh's producer-side attach path takes the sealed value,
// which has no wire decode. Caller cancellation anywhere — discovery
// included — discards the whole partial report and returns only the
// cancellation error (REQ-policy-cancellation). Invocations execute
// sequentially in record order; concurrency lives inside each invocation,
// bounded per package — so each invocation's envelope bounds only its own
// span and the policy's wall time is the sum of what its invocations
// spend, bounded overall only by the caller's context.
func ExecutePolicy(ctx context.Context, pc *Capture) (*stipulatorv1.ExecutionReport, []*ProcessObservation, error) {
	return executePolicy(ctx, pc, nil, nil)
}

// executePolicy is ExecutePolicy with a per-package completion hook:
// onPackage fires, serialized within its invocation, the moment a
// package's process has completed and been classified under the named
// invocation — the seam that lets the package's records install while
// its siblings still execute on this form too
// (REQ-evidence-witness-cache-format-install's install-on-completion rule).
func executePolicy(ctx context.Context, pc *Capture, onPackage func(invocation string, unit packageUnit) error, proofs processProofs) (*stipulatorv1.ExecutionReport, []*ProcessObservation, error) {
	if err := pc.ConfigureExecution(true, nil); err != nil {
		return nil, nil, err
	}
	rep := progress.FromContext(ctx)
	if rep == nil {
		rep = progress.New(nil)
		ctx = progress.NewContext(ctx, rep)
	}
	rep.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	universe, err := pc.ObligationUniverse(ctx)
	if err != nil {
		return nil, nil, err
	}
	d, err := pc.discover(ctx)
	if err != nil {
		return nil, nil, err
	}
	rep.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	var (
		invocations  []*stipulatorv1.InvocationHealth
		tests        []*stipulatorv1.TestResult
		diags        []*stipulatorv1.FailureDiagnostic
		observations []*ProcessObservation
	)
	for _, ic := range d.invocations {
		var hook func(unit packageUnit) error
		if onPackage != nil {
			name := ic.n.Name
			hook = func(unit packageUnit) error { return onPackage(name, unit) }
		}
		health, invTests, invDiags, invObs, err := executeInvocationPrepared(ctx, ic.n, ic.obligations, hook, proofs[ic.n.Name])
		if err != nil {
			return nil, nil, err
		}
		invocations = append(invocations, health)
		tests = append(tests, invTests...)
		diags = append(diags, invDiags...)
		observations = append(observations, invObs...)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	report := &stipulatorv1.ExecutionReport{}
	report.SetInvocations(invocations)
	report.SetTests(tests)
	report.SetObligations(PartitionReports(universe, d.selections()))
	report.SetDiagnostics(diags)
	wire := make([]*stipulatorv1.Observation, len(observations))
	for i, o := range observations {
		wire[i] = o.Wire
	}
	report.SetObservations(wire)
	report.SetTiming(rep.TimingDiagnostics())
	return report, observations, nil
}
