package golang

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/trace"
)

// afterRunnableSnapshotForTest moves source between native capture and the
// closing runnable-inventory observation. It grants no producing capability.
var afterRunnableSnapshotForTest func()

// groupSnapshot observes one compilation generation. Runtime bindings and every
// independently closing attempt share these immutable source/build facts.
func groupSnapshot(ctx context.Context, root string, g *captureGroup) (*witnessSnapshot, *pgoObservation, error) {
	gsStart := time.Now()
	prepared, err := prepareWitnessGeneration(ctx, root, g)
	trace.Emit(ctx, trace.DomainWitness, "group_snapshot", strings.Join(g.invs, ","),
		trace.Int("packages", len(g.packages)),
		trace.Int("solos", len(prepared.soloSubjects)),
		trace.Int64("duration_ms", time.Since(gsStart).Milliseconds()),
		trace.Err(err),
	)
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

func checkSubjectsInMemory(ctx context.Context, snapshot *gofresh.Snapshot, dir string, env []string, subjects []gofresh.Subject) ([]gofresh.CheckedCandidate, error) {
	fps, err := snapshot.CaptureBatch(ctx)
	if err != nil {
		return nil, err
	}
	binding, err := snapshot.BindRuntime(ctx, gofresh.RuntimeDescription{Directory: dir, Environment: env})
	if err != nil {
		return nil, err
	}
	cu, err := binding.BeginCheck(subjects)
	if err != nil {
		return nil, err
	}
	defer cu.Abort()
	cands := make([]gofresh.Candidate, len(subjects))
	for i, s := range subjects {
		cands[i] = gofresh.Candidate{Subject: s, Recorded: fps[s]}
	}
	return cu.CheckBatch(ctx, cands)
}

func captureNativeGeneration(ctx context.Context, root string, g *captureGroup, inputs *pgoObservation, subjects []gofresh.Subject) (*gofresh.Snapshot, *gofresh.Snapshot, map[gofresh.Subject]bool, error) {
	capStart := time.Now()
	var soloCount, candCount int
	defer func() {
		trace.Emit(ctx, trace.DomainWitness, "capture_native_generation", strings.Join(g.invs, ","),
			trace.Int("subjects", len(subjects)),
			trace.Int("candidates", candCount),
			trace.Int("solos", soloCount),
			trace.Int64("duration_ms", time.Since(capStart).Milliseconds()),
		)
	}()
	session, err := groupSession(ctx, root, g, inputs)
	if err != nil {
		return nil, nil, nil, err
	}
	snapshot, err := session.Snapshot(ctx, subjects, gofresh.CodeResult)
	if err != nil {
		return nil, nil, nil, err
	}
	dir := g.directory(root)
	checked, err := checkSubjectsInMemory(ctx, snapshot, dir, g.witnessEnv, subjects)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return snapshot, nil, nil, nil
	}
	var candidates []gofresh.Subject
	for _, c := range checked {
		if c.Verdict.Ground.Cause.Kind == gofresh.SharedDynamicStateCause {
			candidates = append(candidates, c.Subject)
		}
	}
	candCount = len(candidates)
	if len(candidates) == 0 {
		return snapshot, nil, nil, nil
	}
	soloSnapshot, err := snapshot.WithExecution(gofresh.SingleSubjectExecution)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return snapshot, nil, nil, nil
	}
	soloChecked, err := checkSubjectsInMemory(ctx, soloSnapshot, dir, g.witnessEnv, candidates)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return snapshot, nil, nil, nil
	}
	soloSubjects := map[gofresh.Subject]bool{}
	for _, c := range soloChecked {
		if c.Verdict.Ground.Cause.Kind != gofresh.SharedDynamicStateCause {
			soloSubjects[c.Subject] = true
		}
	}
	soloCount = len(soloSubjects)
	if len(soloSubjects) == 0 {
		return snapshot, nil, nil, nil
	}
	return snapshot, soloSnapshot, soloSubjects, nil
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
		sessionState: &groupSessionState{},
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
