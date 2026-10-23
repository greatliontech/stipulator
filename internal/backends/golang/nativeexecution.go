package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	gofresh "github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// packageExecution retains one artifact across independent isolation attempts.
// Its owner keeps the artifact through native closing and publication.
type packageExecution struct {
	built     *retainedBuildResult
	coverage  *retainedCoverageProfile
	artifacts []*retainedArtifact
	attempts  int
	directory string
}

type nativeAttempt struct {
	unit             *gofresh.PackageUnit
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

// finishNativeRun closes after the process slot is released, under the caller's
// context rather than the execution envelope. Captures and artifacts remain
// owned until closing finishes; an earlier completed process does not become a
// timeout merely because publication outlasted its execution budget.
func finishNativeRun(ctx context.Context, run *packageRun) {
	a := run.attempt
	if a == nil {
		return
	}
	// The execution owner releases captures and runtime storage only after the
	// package-completion consumer has selected and persisted its records.
	var final gofresh.FinalizedProcess
	err := a.err
	if err == nil && a.unit != nil {
		final, err = closeNativeProcess(ctx, a.unit)
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
}

func runNativePackage(caller, ctx context.Context, n *NormalizedInvocation, pkg string, selection []string, ordinal int32, gate *admission, leg *packageLeg, execution *packageExecution) (run packageRun) {
	defer func() { run.execution = execution }()
	hooks := retainedProcessHooks{
		Spawned: func(_ string, pid int) { gate.spawned(pkg, pid) },
		Reaped:  func(_ string, pid int, state *os.ProcessState) { gate.reaped(pkg, pid, processPeakBytes(state)) },
	}
	if execution.built == nil {
		if beforePackageBuildForTest != nil {
			beforePackageBuildForTest(pkg)
		}
		leg.prepareBuild(ctx)
		b := buildRetained(ctx, n, pkg, hooks)
		execution.built = &b
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
		return packageRun{pkg: pkg, disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY}
	}
	logPath := ""
	attempt := &nativeAttempt{leg: leg}
	retained := false
	defer func() {
		if !retained {
			attempt.release()
		}
	}()
	if f, err := os.CreateTemp("", "stipulator-testlog-*.txt"); err == nil {
		logPath = f.Name()
		_ = f.Close()
		attempt.logPath = logPath
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
	unit, contribution, prepErr := prepareNativeAttempt(ctx, n, pkg, leg, built.Artifact, runtime, logPath, producer)
	attempt.unit, attempt.err = unit, prepErr
	actual := runRetained(caller, ctx, n, runtime, producer, hooks)
	run = actual.Classify(n, pkg)
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
var closeNativeProcess = func(ctx context.Context, unit *gofresh.PackageUnit) (gofresh.FinalizedProcess, error) {
	return unit.Close(ctx)
}

func prepareNativeAttempt(ctx context.Context, n *NormalizedInvocation, pkg string, leg *packageLeg, artifact *retainedArtifact, runtime *retainedRuntime, logPath string, producer *stipulatorv1.ProducerIdentity) (*gofresh.PackageUnit, gofresh.Contribution, error) {
	if leg == nil {
		if n.PkgDirs[pkg] == "" {
			return nil, gofresh.Contribution{}, fmt.Errorf("package directory unknown at native capture; no observation bracket was captured")
		}
		return nil, gofresh.Contribution{}, fmt.Errorf("package has no native snapshot")
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
	binding := leg.binding
	if runtime.retainedEnvironment != leg.environment {
		var err error
		binding, err = leg.view.BindRuntime(ctx, gofresh.RuntimeDescription{Directory: runtime.Dir, Environment: runtime.Env})
		if err != nil {
			return nil, gofresh.Contribution{}, err
		}
	}
	build, err := leg.view.BindBuilt(pkg, artifact.Path)
	if err != nil {
		return nil, gofresh.Contribution{}, err
	}
	unit, err := binding.BeginPackage(subjects)
	if err != nil {
		return nil, gofresh.Contribution{}, err
	}
	contribution, err := unit.Register(ctx, build, gofresh.ProcessDescription{
		Identity: processIdentity(n, producer, pkg), Capture: logPath,
		BracketPaths: brackets, ExcludedPaths: n.ExcludedPaths, ScratchNamespaces: n.ScratchNamespaces,
	})
	return unit, contribution, err
}
