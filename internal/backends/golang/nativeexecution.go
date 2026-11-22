package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/trace"
)

// packageExecution retains one artifact across independent isolation attempts.
// Its owner keeps the artifact through native closing and publication.
type packageExecution struct {
	built        *retainedBuildResult
	buildElapsed time.Duration
	coverage     *retainedCoverageProfile
	artifacts    []*retainedArtifact
	attempts     int
	directory    string
}

type nativeAttempt struct {
	unit             *gofresh.Unit
	ownedEnvironment *retainedEnvironment
	logPath          string
	leg              *packageLeg
	err              error
	runtime          gofresh.RuntimeDescription
}

func (a *nativeAttempt) release() {
	if a == nil {
		return
	}
	if a.unit != nil {
		a.unit.Abort()
		a.unit = nil
	}
	if a.ownedEnvironment != nil {
		_ = a.ownedEnvironment.Close()
		a.ownedEnvironment = nil
	}
	if a.logPath != "" {
		_ = os.Remove(a.logPath)
		a.logPath = ""
	}
}

func releasePackageUnits(units []packageUnit) {
	for i := range units {
		units[i].run.attempt.release()
		for j := range units[i].solos {
			units[i].solos[j].attempt.release()
		}
	}
}

type packageUnitsConfig struct {
	tests               TestSelection
	proofs              map[string]*packageLeg
	spawn               func() int32
	skipCloseOnPriorErr bool
	followups           func(ctx, invCtx context.Context, unit *packageUnit, gate *admission, spawn func() int32) error
	onPackage           func(unit packageUnit) error
}

// executePackageUnits is the shared package-slot lifecycle for both
// health-judged invocation execution and witness-only selective execution.
// Processes and in-slot follow-up attempts run under invCtx while holding the
// admission slot; native closing runs outside the slot under ctx, parallelized
// across packages unless beforePackagePublishForTest is active, with onPackage
// serialized under mu (REQ-policy-cancellation-publication-slot).
func executePackageUnits(ctx context.Context, n *NormalizedInvocation, pkgs []string, cfg packageUnitsConfig) ([]packageRun, []packageUnit, bool, error) {
	invCtx, cancel := context.WithTimeoutCause(ctx, n.Timeout, errEnvelopeExpired)
	defer cancel()
	spawn := cfg.spawn
	if spawn == nil {
		spawn = spawnOrdinals()
	}
	var (
		mu       sync.Mutex
		firstErr error
		units    = make([]packageUnit, len(pkgs))
	)
	defer releasePackageUnits(units)
	recordErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	inSlot := func(i int, run *packageRun, gate *admission) {
		unit := packageUnit{pkg: pkgs[i], run: *run}
		defer func() { units[i] = unit }()
		if err := finalizeRun(n, &unit.run, invCtx.Err() != nil, ""); err != nil {
			recordErr(err)
			return
		}
		*run = unit.run
		if cfg.followups != nil {
			if err := cfg.followups(ctx, invCtx, &unit, gate, spawn); err != nil {
				recordErr(err)
				return
			}
		}
	}
	afterSlot := func(i int, run *packageRun) {
		mu.Lock()
		failed := firstErr != nil
		hasPublishHook := beforePackagePublishForTest != nil
		mu.Unlock()
		if failed && cfg.skipCloseOnPriorErr {
			return
		}
		finalize := func() {
			finishNativeRun(ctx, &units[i].run)
			for j := range units[i].solos {
				finishNativeRun(ctx, &units[i].solos[j])
			}
			*run = units[i].run
		}
		if hasPublishHook {
			mu.Lock()
			defer mu.Unlock()
			if firstErr != nil && cfg.skipCloseOnPriorErr {
				return
			}
			beforePackagePublishForTest(units[i].pkg)
			finalize()
			if cfg.onPackage != nil && firstErr == nil {
				if err := cfg.onPackage(units[i]); err != nil {
					firstErr = err
				}
			}
			return
		}
		finalize()
		if cfg.onPackage == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if firstErr == nil {
			if err := cfg.onPackage(units[i]); err != nil {
				firstErr = err
			}
		}
	}
	runs := runSelectedPackages(ctx, invCtx, n, pkgs, cfg.tests, spawn, inSlot, afterSlot, cfg.proofs)
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	if firstErr != nil {
		return nil, nil, false, firstErr
	}
	return runs, units, invCtx.Err() != nil, nil
}

func runFollowupAttempt(ctx, invCtx context.Context, n *NormalizedInvocation, pkg string, selection []string, soloTest string, requireLiveEnvelope bool, spawn func() int32, gate *admission, leg *packageLeg, execution *packageExecution, unit *packageUnit) (bool, error) {
	if requireLiveEnvelope && (invCtx.Err() != nil || ctx.Err() != nil) {
		return true, nil
	}
	attempt := packageRun{pkg: pkg, soloTest: soloTest}
	if invCtx.Err() == nil {
		start := time.Now()
		attempt = runNativePackage(ctx, invCtx, n, pkg, selection, spawn(), gate, leg, execution)
		attempt.soloTest = soloTest
		if !requireLiveEnvelope && trace.AnyEnabled() {
			target := pkg
			event := "reduced_batch_exec"
			if soloTest != "" {
				target = pkg + "." + soloTest
				event = "solo_exec"
			}
			trace.Emit(ctx, trace.DomainWitness, event, target,
				trace.String("invocation", n.Name),
				trace.Int("selection", len(selection)),
				trace.String("disposition", attempt.disposition.String()),
				trace.Int64("binary_ms", attempt.binaryElapsed.Milliseconds()),
				trace.Int64("duration_ms", time.Since(start).Milliseconds()),
			)
		}
	}
	if ctx.Err() != nil {
		pt := attempt.buildTimingDiagnostic(n.Name, true)
		progress.FromContext(ctx).RecordPackageTiming(pt)
		unit.solos = append(unit.solos, attempt)
		return true, nil
	}
	if err := finalizeRun(n, &attempt, invCtx.Err() != nil, soloTest); err != nil {
		unit.solos = append(unit.solos, attempt)
		return true, err
	}
	pt := attempt.buildTimingDiagnostic(n.Name, false)
	progress.FromContext(ctx).RecordPackageTiming(pt)
	unit.solos = append(unit.solos, attempt)
	return false, nil
}

// finishNativeRun closes after the process slot is released, under the caller's
// context rather than the execution envelope. Captures and artifacts remain
// owned until closing finishes; an earlier completed process does not become a
// timeout merely because publication outlasted its execution budget.
func finishNativeRun(ctx context.Context, run *packageRun) {
	a := run.attempt
	if a == nil {
		return
	}
	finStart := time.Now()
	// The execution owner releases captures and runtime storage only after the
	// package-completion consumer has selected and persisted its records.
	var final gofresh.Finalized
	err := a.err
	if err == nil && a.unit != nil {
		final, err = closeNativeProcess(ctx, a.unit)
	}
	if a.logPath != "" {
		_ = os.Remove(a.logPath)
		a.logPath = ""
	}
	var account gofresh.ProcessAccount
	if a.unit != nil {
		account, _ = a.unit.Account()
	}
	if err == nil {
		err = a.leg.validateInputs(ctx)
	}
	*run.obs = *nativeObservation(run.pkg, run.producer, final, account, err)
	run.obs.runtime = a.runtime
	target := run.pkg
	if run.soloTest != "" {
		target = run.pkg + "." + run.soloTest
	}
	movedInputs := 0
	var spanErr *gofresh.RuntimeInputSpanError
	if errors.As(err, &spanErr) {
		movedInputs = len(spanErr.MovedInputs)
	}
	trace.Emit(ctx, trace.DomainWitness, "finish_native_run", target,
		trace.Bool("solo", run.soloTest != ""),
		trace.Int("subjects", len(final.Subjects)),
		trace.Int("moved_inputs", movedInputs),
		trace.Int64("duration_ms", time.Since(finStart).Milliseconds()),
		trace.Err(err),
	)
}

func runNativePackage(caller, ctx context.Context, n *NormalizedInvocation, pkg string, selection []string, ordinal int32, gate *admission, leg *packageLeg, execution *packageExecution) (run packageRun) {
	runStart := time.Now()
	defer func() {
		run.execution = execution
		run.buildElapsed = execution.buildElapsed
		run.totalElapsed = time.Since(runStart)
	}()
	hooks := retainedProcessHooks{
		Spawned: func(_ string, pid int) { gate.spawned(pkg, pid) },
		Reaped:  func(_ string, pid int, state *os.ProcessState) { gate.reapedState(pkg, pid, state) },
	}
	if execution.built == nil {
		if beforePackageBuildForTest != nil {
			beforePackageBuildForTest(pkg)
		}
		leg.prepareBuild(ctx)
		buildStart := time.Now()
		b := buildRetained(ctx, n, pkg, hooks)
		execution.buildElapsed = time.Since(buildStart)
		execution.built = &b
		trace.Emit(caller, trace.DomainWitness, "package_build", pkg,
			trace.String("invocation", n.Name),
			trace.Bool("no_test_files", b.NoTestFiles),
			trace.Int64("duration_ms", execution.buildElapsed.Milliseconds()),
			trace.Err(b.Err),
		)
	}
	built, coverage, artifacts := execution.built, execution.coverage, execution.artifacts
	if ctx.Err() != nil {
		return packageRun{pkg: pkg}
	}
	if built.Err != nil {
		if built.Stream != nil {
			return classifyRun(n.Name, pkg, built.Stream, built.Err, &built.Stderr, "")
		}
		return degradedRun(n.Name, pkg, "retained compilation: "+built.Err.Error(), false)
	}
	if built.NoTestFiles {
		if err := mergeRetainedBuildCoverage(ctx, n, built.Artifact, coverage, hooks); err != nil {
			return degradedRun(n.Name, pkg, err.Error(), false)
		}
		return packageRun{pkg: pkg, noTestFiles: true, disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY}
	}
	logPath := ""
	var logErr error
	attempt := &nativeAttempt{leg: leg}
	retained := false
	defer func() {
		if !retained {
			attempt.release()
		}
	}()
	if leg != nil {
		if f, err := os.CreateTemp("", "stipulator-testlog-*.txt"); err != nil {
			logErr = err
		} else {
			logPath = f.Name()
			if closeErr := f.Close(); closeErr != nil {
				_ = os.Remove(logPath)
				logPath = ""
				logErr = closeErr
			} else {
				attempt.logPath = logPath
			}
		}
	}
	var environment *retainedEnvironment
	if execution.attempts == 0 && leg != nil {
		environment = leg.environment
	}
	execution.attempts++
	if environment == nil {
		var err error
		directory := execution.directory
		if directory == "" {
			directory = n.PkgDirs[pkg]
		}
		environment, err = prepareRetainedEnvironment(n, directory, n.GOROOT)
		if err != nil {
			return degradedRun(n.Name, pkg, err.Error(), false)
		}
		attempt.ownedEnvironment = environment
	}
	runtime, err := prepareRetainedRuntime(ctx, n, built.Artifact, retainedRuntimeOptions{
		Package: pkg, Environment: environment, GOCACHE: n.GOCACHE,
		Selection: selection, LogPath: logPath, Coverage: coverage, CoverageArtifacts: artifacts,
	}, hooks)
	if err != nil {
		if ctx.Err() != nil {
			return packageRun{pkg: pkg}
		}
		return degradedRun(n.Name, pkg, err.Error(), false)
	}
	if err := requireExactInputEligibility(ctx, n, runtime.retainedEnvironment); err != nil {
		return degradedRun(n.Name, pkg, err.Error(), false)
	}
	attempt.runtime = gofresh.RuntimeDescription{Directory: runtime.Dir, Environment: slices.Clone(runtime.Env)}
	producer := &stipulatorv1.ProducerIdentity{}
	producer.SetInvocation(n.Name)
	producer.SetProcessOrdinal(ordinal)
	prepStart := time.Now()
	var (
		unit         *gofresh.Unit
		contribution gofresh.Contribution
		prepErr      error
	)
	if logErr != nil {
		prepErr = fmt.Errorf("creating testlog capture: %w", logErr)
	} else {
		unit, contribution, prepErr = prepareNativeAttempt(ctx, n, pkg, leg, built.Artifact, runtime, logPath, producer)
	}
	prepElapsed := time.Since(prepStart)
	attempt.unit, attempt.err = unit, prepErr
	binStart := time.Now()
	actual := runRetained(caller, ctx, n, runtime, producer, hooks)
	binElapsed := time.Since(binStart)
	run = actual.Classify(n, pkg)
	run.binaryElapsed = binElapsed
	trace.Emit(caller, trace.DomainWitness, "package_exec", pkg,
		trace.String("invocation", n.Name),
		trace.Int("selection", len(selection)),
		trace.String("disposition", run.disposition.String()),
		trace.Int64("prep_ms", prepElapsed.Milliseconds()),
		trace.Int64("binary_ms", binElapsed.Milliseconds()),
		trace.Err(prepErr),
	)
	if actual.Producer == nil {
		if prepErr == nil && unit != nil {
			_ = unit.Submit(contribution, gofresh.Terminal{Kind: gofresh.NeverStarted, Reason: "retained test binary did not start"})
		}
		return run
	}
	retained = true
	run.attempt = attempt
	run.obs = incompleteObservation(pkg, producer, "native observation awaits closing")
	if prepErr != nil || unit == nil {
		return run
	}
	terminal := gofresh.Terminal{Kind: gofresh.AbnormalCompletion, Reason: "retained test process did not establish ordinary harness completion"}
	if actual.Stream != nil {
		if reason := incompleteObservationReason(actual.Stream, actual.BinaryErr, run.disposition); reason != "" {
			terminal.Reason = reason
		}
	}
	if actual.HarnessComplete() {
		terminal = gofresh.Terminal{Kind: gofresh.NormalCompletion}
	} else if actual.Interrupted && errors.Is(context.Cause(ctx), errEnvelopeExpired) {
		terminal.Reason = fmt.Sprintf("invocation timeout %v expired before the process completed", n.Timeout)
	}
	attempt.err = unit.Submit(contribution, terminal)
	return run
}

// This seam injects an infrastructure closing fault without manufacturing an
// executed fingerprint or weakening the real process's terminal assertion.
var closeNativeProcess = func(ctx context.Context, unit *gofresh.Unit) (gofresh.Finalized, error) {
	return unit.Close(ctx)
}

func prepareNativeAttempt(ctx context.Context, n *NormalizedInvocation, pkg string, leg *packageLeg, artifact *retainedArtifact, runtime *retainedRuntime, logPath string, producer *stipulatorv1.ProducerIdentity) (*gofresh.Unit, gofresh.Contribution, error) {
	if leg == nil {
		if n.PkgDirs[pkg] == "" {
			return nil, gofresh.Contribution{}, fmt.Errorf("package directory unknown at native capture; no observation bracket was captured")
		}
		return nil, gofresh.Contribution{}, fmt.Errorf("package has no native snapshot")
	}
	if logPath == "" {
		return nil, gofresh.Contribution{}, errors.New("witnessed native attempt requires a testlog capture path")
	}
	leg.preparation.Lock()
	defer leg.preparation.Unlock()
	if leg.view == nil {
		return nil, gofresh.Contribution{}, fmt.Errorf("package snapshot released")
	}
	if leg.inputErr != nil {
		return nil, gofresh.Contribution{}, leg.inputErr
	}
	if artifact.BindingError != nil {
		return nil, gofresh.Contribution{}, artifact.BindingError
	}
	names, err := leg.view.roster.currentNames(ctx, pkg)
	if err != nil {
		return nil, gofresh.Contribution{}, err
	}
	subjects, err := nativeRunnableSubjects(names, pkg, runtime.Args)
	if err != nil {
		return nil, gofresh.Contribution{}, err
	}
	if len(subjects) == 0 {
		return nil, gofresh.Contribution{}, fmt.Errorf("package has no actual runnable subjects")
	}
	brackets, reason := bracketRootsFor(n, pkg)
	if reason != "" {
		return nil, gofresh.Contribution{}, fmt.Errorf("%s", reason)
	}
	snapshot := leg.view.Snapshot
	binding := leg.binding
	if len(subjects) == 1 && leg.view.isSolo(subjects[0]) {
		snapshot = leg.view.solo
		binding = leg.soloBinding
	}
	if runtime.retainedEnvironment != leg.environment || binding == nil {
		var err error
		binding, err = snapshot.BindRuntime(ctx, gofresh.RuntimeDescription{Directory: runtime.Dir, Environment: runtime.Env})
		if err != nil {
			return nil, gofresh.Contribution{}, err
		}
	}
	build, err := snapshot.BindBuilt(pkg, artifact.Path)
	if err != nil {
		return nil, gofresh.Contribution{}, err
	}
	var unit *gofresh.Unit
	if len(subjects) == 1 {
		unit, err = binding.Begin(subjects[0])
	} else {
		unit, err = binding.BeginPackage(subjects)
	}
	if err != nil {
		return nil, gofresh.Contribution{}, err
	}
	contribution, err := unit.Register(ctx, build, gofresh.ProcessDescription{
		Identity: processIdentity(n, producer, pkg), Capture: logPath,
		BracketPaths: brackets, ExcludedPaths: runnerExcludedPaths(n, artifact, runtime),
		ScratchNamespaces: n.ScratchNamespaces,
	})
	if trace.AnyEnabled() {
		needsObservation, _ := binding.NeedsObservation(subjects)
		trace.Emit(ctx, trace.DomainWitness, "prepare_native_attempt", pkg,
			trace.String("invocation", n.Name),
			trace.Int("subjects", len(subjects)),
			trace.Bool("single_subject", len(subjects) == 1),
			trace.Bool("needs_observation", needsObservation),
			trace.Err(err),
		)
	}
	return unit, contribution, err
}

func appendRunnerExclusion(out []string, tree string, raw string) []string {
	if raw == "" || !filepath.IsAbs(raw) {
		return out
	}
	clean := filepath.Clean(raw)
	candidates := []string{clean}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		if resolved != clean {
			candidates = append(candidates, resolved)
		}
	} else if resolvedParent, pErr := filepath.EvalSymlinks(filepath.Dir(clean)); pErr == nil {
		resolved := filepath.Join(resolvedParent, filepath.Base(clean))
		if resolved != clean {
			candidates = append(candidates, resolved)
		}
	}
	effectiveTree := ""
	if tree != "" && filepath.IsAbs(tree) {
		cleanTree := filepath.Clean(tree)
		effectiveTree = cleanTree
		if resolvedTree, err := filepath.EvalSymlinks(cleanTree); err == nil {
			effectiveTree = resolvedTree
		}
	}
	for _, cand := range candidates {
		if effectiveTree != "" {
			if rel, err := filepath.Rel(effectiveTree, cand); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				slash := filepath.ToSlash(rel)
				if !slices.Contains(out, slash) {
					out = append(out, slash)
				}
				continue
			}
		}
		if !slices.Contains(out, cand) {
			out = append(out, cand)
		}
	}
	return out
}

func runnerEnvironmentExcludedPaths(n *NormalizedInvocation, env []string) []string {
	if n == nil {
		return nil
	}
	out := slices.Clone(n.ExcludedPaths)
	tree := treeRoot(n)
	if n.TelemetrySource != "" {
		if cfgHome, ok := lookupEnv(env, "XDG_CONFIG_HOME"); ok && cfgHome != "" {
			out = appendRunnerExclusion(out, tree, cfgHome)
		}
	}
	return out
}

func runnerExcludedPaths(n *NormalizedInvocation, artifact *retainedArtifact, runtime *retainedRuntime) []string {
	var env []string
	if runtime != nil && runtime.retainedEnvironment != nil {
		env = runtime.Env
	} else if n != nil {
		env = n.Env
	}
	out := runnerEnvironmentExcludedPaths(n, env)
	var tree string
	if n != nil {
		tree = treeRoot(n)
	}
	if artifact != nil {
		out = appendRunnerExclusion(out, tree, artifact.root)
		out = appendRunnerExclusion(out, tree, artifact.Path)
		out = appendRunnerExclusion(out, tree, artifact.work)
	}
	if runtime != nil {
		if runtime.retainedEnvironment != nil {
			out = appendRunnerExclusion(out, tree, runtime.root)
		}
		out = appendRunnerExclusion(out, tree, runtime.CoverageFragment)
	}
	return out
}
