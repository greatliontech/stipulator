package golang

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/progress"
)

// afterRunnableSnapshotForTest moves source between native capture and the
// closing runnable-inventory observation. It grants no producing capability.
var afterRunnableSnapshotForTest func()

// groupSnapshot observes one compilation generation. Runtime bindings and every
// independently closing attempt share these immutable source/build facts.
func groupSnapshot(ctx context.Context, root string, g *captureGroup) (*witnessSnapshot, *pgoObservation, error) {
	prepared, err := prepareWitnessGeneration(ctx, root, g)
	if err != nil {
		return nil, prepared.inputs, err
	}
	return &witnessSnapshot{Snapshot: prepared.native, roster: prepared.population}, prepared.inputs, nil
}

// witnessPreparation keeps observed population facts independently of native
// capability. A present population has passed its closing agreement observation,
// including when empty; a missing native snapshot grants no native methods.
type witnessPreparation struct {
	population *runnableGeneration
	native     *gofresh.Snapshot
	inputs     *pgoObservation
}

func prepareWitnessGeneration(ctx context.Context, root string, g *captureGroup) (witnessPreparation, error) {
	if beforeGroupEngineForTest != nil {
		beforeGroupEngineForTest()
	}
	if err := checkToolchainProvenance(ctx, g.directory(root), g.env, groupSample(g)); err != nil {
		return witnessPreparation{}, err
	}
	inputs, nativeErr := observePGO(ctx, root, g)
	if ctx.Err() != nil {
		return witnessPreparation{}, ctx.Err()
	}
	query := NormalizedInvocation{Name: strings.Join(g.invs, ","), Dir: g.directory(root), Env: slices.Clone(g.witnessEnv), Tags: slices.Clone(g.tags), Race: g.race, ModuleMode: g.moduleMode, TelemetrySource: g.telemetrySource}
	for pkg := range g.packages {
		query.Packages = append(query.Packages, pkg)
	}
	slices.Sort(query.Packages)
	roster, err := observeRunnableGeneration(ctx, query)
	if err != nil {
		return witnessPreparation{}, errors.Join(nativeErr, err)
	}
	subjects := roster.subjects()
	var snapshot *gofresh.Snapshot
	if nativeErr == nil {
		if len(subjects) == 0 {
			nativeErr = fmt.Errorf("native source generation has no runnable subjects")
		} else {
			snapshot, nativeErr = captureNativeGeneration(ctx, root, g, inputs, subjects)
		}
	}
	if ctx.Err() != nil {
		return witnessPreparation{}, ctx.Err()
	}
	if snapshot != nil && afterRunnableSnapshotForTest != nil {
		afterRunnableSnapshotForTest()
	}
	// This endpoint is required independently of native construction. Neither
	// an empty population nor a construction refusal can turn an unfinished
	// first inventory read into absence evidence.
	if err := roster.unchanged(ctx); err != nil {
		return witnessPreparation{}, errors.Join(nativeErr, err)
	}
	return witnessPreparation{population: roster, native: snapshot, inputs: inputs}, nativeErr
}

func captureNativeGeneration(ctx context.Context, root string, g *captureGroup, inputs *pgoObservation, subjects []gofresh.Subject) (*gofresh.Snapshot, error) {
	policy := gofresh.SessionPolicy{
		EvidenceRoot: root, Vouches: slices.Clone(g.vouches), Execution: gofresh.PackageExecution,
		Runner:   engineRunner,
		Progress: func(p gofresh.Progress) { emitEngineDiagnostic(p); progress.FromContext(ctx).Keepalive() },
	}
	if g.assumePure {
		policy.PureSubjects = subjects
	}
	session, err := gofresh.NewSession(ctx, gofresh.BuildDescription{
		Directory: g.directory(root), Environment: g.witnessEnv,
		Flags:  append(buildFlags(g.race, g.tags, g.moduleMode, buildPGO(root, g.pgo)), linkerBuildFlags(g.linkerAssignments)...),
		Inputs: append([]string{}, inputs.inputs...),
	}, policy)
	if err != nil {
		return nil, err
	}
	return session.Snapshot(ctx, subjects, gofresh.CodeResult)
}

// nativePackageLeg binds checking to the same runtime coordinate that the
// retained executor uses, including its prospective coverage directory.
func nativePackageLeg(ctx context.Context, snapshot *witnessSnapshot, inputs *pgoObservation, n *NormalizedInvocation, pkg string) (*packageLeg, error) {
	if snapshot.roster == nil {
		return nil, fmt.Errorf("native snapshot has no runnable generation")
	}
	inventory, ok := snapshot.roster.packages[pkg]
	if !ok {
		return nil, fmt.Errorf("package %s is outside the native runnable generation", pkg)
	}
	environment, err := prepareRetainedEnvironment(n, inventory.dir, n.GOROOT)
	if err != nil {
		return nil, err
	}
	if err := requireExactInputEligibility(ctx, n, environment); err != nil {
		_ = environment.Close()
		return nil, err
	}
	binding, err := snapshot.BindRuntime(ctx, gofresh.RuntimeDescription{Directory: environment.Dir, Environment: environment.Env})
	if err != nil {
		_ = environment.Close()
		return nil, err
	}
	return &packageLeg{view: snapshot, binding: binding, environment: environment, inputs: inputs}, nil
}

// executionLegs provides native process accounts for the execution-only surface.
// It publishes nothing. Failed preparation leaves execution and health intact,
// with the attempted process explicitly lacking reusable preparation.
func executionLegs(ctx context.Context, n *NormalizedInvocation, packages []string) map[string]*packageLeg {
	g := &captureGroup{
		toolchain: n.Toolchain, telemetrySource: n.TelemetrySource, tags: n.Tags, env: n.Env,
		witnessEnv: witnessEnvOf(n), race: n.Race, moduleRoot: n.ModuleRoot, moduleMode: n.ModuleMode,
		pgo: n.PGO, assumePure: n.AssumePure, vouches: n.Vouches, selectedPackages: n.selectedPackages,
		linkerAssignments: slices.Clone(n.LinkerAssignments),
		invs:              []string{n.Name}, packages: map[string]*groupPackage{},
	}
	for _, pkg := range packages {
		if len(n.runnables[pkg]) != 0 {
			g.packages[pkg] = &groupPackage{inv: n.Name, names: n.runnables[pkg]}
		}
	}
	legs := map[string]*packageLeg{}
	if len(g.packages) == 0 {
		return legs
	}
	snapshot, inputs, err := groupSnapshot(ctx, treeRoot(n), g)
	if err != nil {
		return legs
	}
	for pkg := range g.packages {
		if leg, err := nativePackageLeg(ctx, snapshot, inputs, n, pkg); err == nil {
			legs[pkg] = leg
		}
	}
	return legs
}
