package golang

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/greatliontech/gofresh"
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
		units    = make([]packageUnit, len(pkgs))
		spawn    = spawnOrdinals()
	)
	defer func() {
		for i := range units {
			for j := range units[i].solos {
				units[i].solos[j].attempt.release()
			}
		}
	}()
	inSlot := func(i int, run *packageRun, gate *admission) {
		unit := packageUnit{pkg: pkgs[i], run: *run}
		defer func() { mu.Lock(); units[i] = unit; mu.Unlock() }()
		if err := finalizeRun(n, &unit.run, invCtx.Err() != nil, ""); err != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
			return
		}
		*run = unit.run
		if onPackage == nil || unit.run.producer == nil || unit.run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
			return
		}
		leg := proofs[unit.pkg]
		if leg == nil || leg.view == nil || leg.view.roster == nil || !leg.view.hasSoloInPackage(unit.pkg) {
			return
		}
		invPkg := leg.view.roster.packages[unit.pkg]
		if len(invPkg.runnables) <= 1 {
			return
		}
		for _, ob := range invPkg.runnables {
			if !leg.view.isSolo(gofresh.Subject{Package: unit.pkg, Symbol: ob.Name}) {
				continue
			}
			if invCtx.Err() != nil || ctx.Err() != nil {
				return
			}
			solo := runNativePackage(ctx, invCtx, n, unit.pkg, []string{ob.Name}, spawn(), gate, leg, unit.run.execution)
			solo.soloTest = ob.Name
			if ctx.Err() != nil {
				pt := solo.buildTimingDiagnostic(n.Name, true)
				progress.FromContext(ctx).RecordPackageTiming(pt)
				unit.solos = append(unit.solos, solo)
				return
			}
			if err := finalizeRun(n, &solo, invCtx.Err() != nil, ob.Name); err != nil {
				unit.solos = append(unit.solos, solo)
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			pt := solo.buildTimingDiagnostic(n.Name, false)
			progress.FromContext(ctx).RecordPackageTiming(pt)
			unit.solos = append(unit.solos, solo)
		}
	}
	afterSlot := func(i int, run *packageRun) {
		mu.Lock()
		defer mu.Unlock()
		if firstErr != nil {
			return
		}
		if beforePackagePublishForTest != nil {
			beforePackagePublishForTest(run.pkg)
		}
		finishNativeRun(ctx, run)
		unit := units[i]
		unit.run = *run
		for j := range unit.solos {
			finishNativeRun(ctx, &unit.solos[j])
		}
		units[i] = unit
		if onPackage == nil {
			return
		}
		if err := onPackage(unit); err != nil {
			firstErr = err
		}
	}
	runs := runSelectedPackages(ctx, invCtx, n, pkgs, nil, spawn, inSlot, afterSlot, proofs)
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
