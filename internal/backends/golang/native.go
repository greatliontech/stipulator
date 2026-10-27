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
	return &witnessSnapshot{
		Snapshot:     prepared.native,
		solo:         prepared.solo,
		soloSubjects: prepared.soloSubjects,
		roster:       prepared.population,
	}, prepared.inputs, nil
}

// witnessPreparation keeps observed population facts independently of native
// capability. A present population has passed its closing agreement observation,
// including when empty; a missing native snapshot grants no native methods.
type witnessPreparation struct {
	population   *runnableGeneration
	native       *gofresh.Snapshot
	solo         *gofresh.Snapshot
	soloSubjects map[gofresh.Subject]bool
	inputs       *pgoObservation
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
	var snapshot, solo *gofresh.Snapshot
	var soloSubjects map[gofresh.Subject]bool
	if nativeErr == nil {
		if len(subjects) == 0 {
			nativeErr = fmt.Errorf("native source generation has no runnable subjects")
		} else {
			snapshot, solo, soloSubjects, nativeErr = captureNativeGeneration(ctx, root, g, inputs, subjects)
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
	return witnessPreparation{population: roster, native: snapshot, solo: solo, soloSubjects: soloSubjects, inputs: inputs}, nativeErr
}

func captureNativeGeneration(ctx context.Context, root string, g *captureGroup, inputs *pgoObservation, subjects []gofresh.Subject) (*gofresh.Snapshot, *gofresh.Snapshot, map[gofresh.Subject]bool, error) {
	policy := gofresh.SessionPolicy{
		EvidenceRoot: root, Vouches: slices.Clone(g.vouches), Execution: gofresh.PackageExecution,
		Runner:   engineRunner,
		Progress: func(p gofresh.Progress) { emitEngineDiagnostic(p); progress.FromContext(ctx).Keepalive() },
	}
	if g.assumePure {
		policy.PureSubjects = subjects
	}
	buildDesc := gofresh.BuildDescription{
		Directory: g.directory(root), Environment: g.witnessEnv,
		Flags:  append(buildFlags(g.race, g.tags, g.moduleMode, buildPGO(root, g.pgo)), linkerBuildFlags(g.linkerAssignments)...),
		Inputs: append([]string{}, inputs.inputs...),
	}
	session, err := gofresh.NewSession(ctx, buildDesc, policy)
	if err != nil {
		return nil, nil, nil, err
	}
	snapshot, err := session.Snapshot(ctx, subjects, gofresh.CodeResult)
	if err != nil {
		return nil, nil, nil, err
	}
	fps, err := snapshot.CaptureBatch(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return snapshot, nil, nil, nil
	}
	probeRuntime := gofresh.RuntimeDescription{Directory: g.directory(root), Environment: g.witnessEnv}
	binding, err := snapshot.BindRuntime(ctx, probeRuntime)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return snapshot, nil, nil, nil
	}
	batchCands := make([]gofresh.Candidate, 0, len(subjects))
	for _, s := range subjects {
		batchCands = append(batchCands, gofresh.Candidate{Subject: s, Recorded: fps[s]})
	}
	verdicts, err := binding.CheckBatchWithLedgers(ctx, batchCands, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return snapshot, nil, nil, nil
	}
	var candidates []gofresh.Subject
	for _, v := range verdicts {
		if v.Verdict.Ground.Cause.Kind == gofresh.SharedDynamicStateCause {
			candidates = append(candidates, v.Subject)
		}
	}
	if len(candidates) == 0 {
		return snapshot, nil, nil, nil
	}
	soloPolicy := policy
	soloPolicy.Execution = gofresh.SingleSubjectExecution
	if g.assumePure {
		soloPolicy.PureSubjects = candidates
	}
	soloSession, err := gofresh.NewSession(ctx, buildDesc, soloPolicy)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return snapshot, nil, nil, nil
	}
	candSnapshot, err := soloSession.Snapshot(ctx, candidates, gofresh.CodeResult)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return snapshot, nil, nil, nil
	}
	soloFPs, err := candSnapshot.CaptureBatch(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return snapshot, nil, nil, nil
	}
	soloBinding, err := candSnapshot.BindRuntime(ctx, probeRuntime)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return snapshot, nil, nil, nil
	}
	soloCands := make([]gofresh.Candidate, 0, len(candidates))
	for _, s := range candidates {
		soloCands = append(soloCands, gofresh.Candidate{Subject: s, Recorded: soloFPs[s]})
	}
	soloVerdicts, err := soloBinding.CheckBatchWithLedgers(ctx, soloCands, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return snapshot, nil, nil, nil
	}
	soloSubjects := map[gofresh.Subject]bool{}
	for _, v := range soloVerdicts {
		if v.Verdict.Ground.Cause.Kind != gofresh.SharedDynamicStateCause {
			soloSubjects[v.Subject] = true
		}
	}
	if len(soloSubjects) == 0 {
		return snapshot, nil, nil, nil
	}
	return snapshot, candSnapshot, soloSubjects, nil
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
	desc := gofresh.RuntimeDescription{Directory: environment.Dir, Environment: environment.Env}
	binding, err := snapshot.BindRuntime(ctx, desc)
	if err != nil {
		_ = environment.Close()
		return nil, err
	}
	var soloBinding *gofresh.RuntimeBinding
	if snapshot.hasSoloInPackage(pkg) {
		soloBinding, err = snapshot.solo.BindRuntime(ctx, desc)
		if err != nil {
			_ = environment.Close()
			return nil, err
		}
	}
	return &packageLeg{view: snapshot, binding: binding, soloBinding: soloBinding, environment: environment, inputs: inputs}, nil
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
