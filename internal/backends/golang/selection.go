package golang

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/trace"
)

// The selective executor is the witness-only execution surface: it runs a
// caller-chosen subset of one normalized invocation's packages, each narrowed
// to named top-level runnables, and grants witness evidence, never suite
// health (REQ-core-one-execution's witness-only selective execution). What it
// adds over plain package execution is the isolation pass: a selective process
// can deny its sibling tests an outcome — a package abort shadows tests that
// never reached a terminal event, and a red process yields no green evidence
// for the passes that completed inside it — so denied tests are isolated inside
// the same invocation envelope (trying one reduced batch of multiple
// batch-eligible passes after a clean top-level test failure before falling
// back to one top-level runnable per process). Each isolated outcome is a real
// run's outcome from its own producing process
// (REQ-evidence-witness-freshness-isolation, REQ-policy-attribution); the
// denying process's own failures stand untouched.

// TestSelection narrows an execution to named top-level runnables per
// package: each key is a package import path, its value the top-level
// Test and Fuzz function names that package's process executes (a
// single-element Fuzz selection replays the target's committed seeds). A
// nil map — or a nil entry — selects the whole package.
type TestSelection map[string][]string

// ProcessOutcome is one process of a selective execution: which package
// it ran, the single runnable it isolated (empty for a package-selection
// or reduced-batch isolation process), its terminal disposition, and its
// producer identity — nil exactly when the envelope denied the process
// before it spawned.
type ProcessOutcome struct {
	Package     string
	Test        string
	Disposition stipulatorv1.HealthDisposition
	Producer    *stipulatorv1.ProducerIdentity
}

// SelectionResult is one witness-only selective execution's report:
// named test outcomes and failure diagnostics attributed to their
// producing processes, one owned observation per launched process, and
// every process's terminal disposition — each package's initial process
// followed by its isolation processes in execution order.
// It deliberately carries no invocation health: a selective execution
// grants witness evidence and never any health (REQ-core-one-execution),
// so consumers key on per-process dispositions, not a suite verdict.
type SelectionResult struct {
	Tests        []*stipulatorv1.TestResult
	Diagnostics  []*stipulatorv1.FailureDiagnostic
	Observations []*ProcessObservation
	Processes    []ProcessOutcome
	Timing       []*stipulatorv1.PackageTimingDiagnostic
}

// ExecuteSelection executes a witness-only selection of one normalized
// invocation: the selected packages, each narrowed to its top-level
// runnables, one owned retained test process per package under the
// invocation's reviewed envelope timeout, followed by the isolation pass
// — tests a red or aborted process denied an outcome are isolated inside
// the same envelope context (one reduced batch when multiple batch-eligible
// tests were denied by a clean top-level failure, else solo per test), so
// retries can never outlive the invocation's reviewed bound. Isolated
// outcomes join the report attributed to their own producing process.
// Caller cancellation discards the partial run: the return is (nil, ctx.Err()),
// never a partial report (REQ-policy-cancellation). Envelope expiry is a
// terminal fact: cut-off processes and re-runs the expired envelope
// denied are reported as TIMEOUT process outcomes, never retried
// outside it.
//
// A nil package entry (whole-package selection) weakens the denial
// bound: a test the process died before ever starting emits no event, so
// without the explicit name list it cannot be counted denied and gains
// no isolation re-run. Explicit per-test names give the full bound —
// every selected name either reaches a terminal event or is denied.
func ExecuteSelection(ctx context.Context, n *NormalizedInvocation, sel TestSelection) (*SelectionResult, error) {
	return ExecuteSelectionObserved(ctx, n, sel, nil)
}

// packageUnit is one package's completed execution: its process's run
// and the solo re-runs its denied tests earned, in the order they ran —
// the unit of persistence (REQ-policy-cancellation-unit).
type packageUnit struct {
	pkg   string
	run   packageRun
	solos []packageRun
}

// ExecuteSelectionObserved is ExecuteSelection with a per-package completion
// hook: onPackage fires, serialized, the moment a package's process and its
// own isolation re-runs have completed and been classified — while other
// packages still execute — so a caller can persist that package's evidence
// before the invocation ends. The hook runs after the package's spawn slot is
// released: the re-runs spawn and so hold the slot, the caller's publication
// spawns nothing and holds none, so a sibling still queued is never delayed by
// it and the envelope bounds processes alone
// (REQ-policy-explicit-time-bounds). A hook error ends the invocation with it.
// A nil hook is ExecuteSelection.
func ExecuteSelectionObserved(ctx context.Context, n *NormalizedInvocation, sel TestSelection, onPackage func(unit packageUnit) error) (*SelectionResult, error) {
	freezeWitnessAllocation(n, len(sel))
	var packages []string
	for pkg := range sel {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)
	legs := executionLegs(ctx, n, packages)
	defer func() {
		for _, leg := range legs {
			leg.dispose()
		}
	}()
	return executeSelectionObserved(ctx, n, sel, onPackage, legs, spawnOrdinals())
}

func partitionSelectionBatch(n *NormalizedInvocation, leg *packageLeg, pkg string, names []string) (batch, solos []string) {
	if leg == nil || leg.view == nil || len(names) <= 1 {
		return names, nil
	}
	view := leg.view
	var fuzzes []string
	for _, name := range names {
		switch {
		case view.isSolo(gofresh.Subject{Package: pkg, Symbol: name}):
			solos = append(solos, name)
		case strings.HasPrefix(name, "Fuzz"):
			fuzzes = append(fuzzes, name)
		default:
			batch = append(batch, name)
		}
	}
	if !n.AssumePure && len(batch) > 0 && len(fuzzes) > 0 {
		solos = append(solos, fuzzes...)
	} else {
		batch = append(batch, fuzzes...)
	}
	return batch, solos
}

func hasFailedTopLevel(r *packageRun) bool {
	for _, tr := range r.tests {
		if tr.GetTest() == topLevel(tr.GetTest()) && tr.GetOutcome() == stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
			return true
		}
	}
	return false
}

func shouldRetryReducedBatch(r *packageRun, batchDenied []string) bool {
	return len(batchDenied) >= 2 &&
		r.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED &&
		len(r.aborted) == 0 &&
		hasFailedTopLevel(r)
}

func executeSelectionObserved(ctx context.Context, n *NormalizedInvocation, sel TestSelection, onPackage func(unit packageUnit) error, proofs map[string]*packageLeg, spawn func() int32) (*SelectionResult, error) {
	pkgs := make([]string, 0, len(sel))
	for pkg := range sel {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("invocation %q: selection names no packages", n.Name)
	}
	// When a package selection mixes Test* and Fuzz* runnables under a
	// native witness snapshot, or contains subjects partitioned into solo
	// SingleSubjectExecution, run those targets as solo processes in the
	// package's slot reusing the compiled binary.
	mainSel := make(TestSelection, len(sel))
	plannedSolos := map[string][]string{}
	for _, pkg := range pkgs {
		names := sel[pkg]
		var leg *packageLeg
		if proofs != nil {
			leg = proofs[pkg]
		}
		batch, solos := partitionSelectionBatch(n, leg, pkg, names)
		if len(solos) > 0 {
			if len(batch) > 0 {
				mainSel[pkg] = batch
				plannedSolos[pkg] = solos
			} else {
				mainSel[pkg] = solos[:1]
				plannedSolos[pkg] = solos[1:]
			}
			if trace.AnyEnabled() {
				for _, name := range plannedSolos[pkg] {
					cause := "solo_subject"
					if !leg.view.isSolo(gofresh.Subject{Package: pkg, Symbol: name}) && strings.HasPrefix(name, "Fuzz") {
						cause = "mixed_fuzz"
					}
					trace.Emit(ctx, trace.DomainWitness, "solo_plan", pkg+"."+name,
						trace.String("invocation", n.Name),
						trace.String("mode", "planned"),
						trace.String("cause", cause),
					)
				}
			}
			continue
		}
		mainSel[pkg] = names
	}
	_, units, _, err := executePackageUnits(ctx, n, pkgs, packageUnitsConfig{
		tests:     mainSel,
		proofs:    proofs,
		spawn:     spawn,
		onPackage: onPackage,
		followups: func(ctx, invCtx context.Context, unit *packageUnit, gate *admission, spawn func() int32) error {
			// A package whose process never ran — refused at admission,
			// degraded at the spawn, never spawned — has no outcome to
			// isolate: its re-runs would be the spawns the refusal exists to
			// withhold, and would grant outcomes a refused package never
			// earned.
			if unit.run.producer == nil {
				return nil
			}
			var leg *packageLeg
			if proofs != nil {
				leg = proofs[unit.pkg]
			}
			denied := deniedTests(&unit.run, mainSel[unit.pkg])
			batchDenied, soloDenied := partitionSelectionBatch(n, leg, unit.pkg, denied)
			if shouldRetryReducedBatch(&unit.run, batchDenied) && invCtx.Err() == nil && ctx.Err() == nil {
				if trace.AnyEnabled() {
					trace.Emit(ctx, trace.DomainWitness, "reduced_batch_plan", unit.pkg,
						trace.String("invocation", n.Name),
						trace.Int("subjects", len(batchDenied)),
						trace.String("cause", unit.run.disposition.String()),
					)
				}
				stopped, err := runFollowupAttempt(ctx, invCtx, n, unit.pkg, batchDenied, "", false, spawn, gate, leg, unit.run.execution, unit)
				if stopped || err != nil {
					return err
				}
				reduced := &unit.solos[len(unit.solos)-1]
				if reduced.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
					denied = soloDenied
				} else {
					denied = append(deniedTests(reduced, batchDenied), soloDenied...)
					sort.Strings(denied)
				}
			}
			if trace.AnyEnabled() {
				for _, name := range denied {
					trace.Emit(ctx, trace.DomainWitness, "solo_plan", unit.pkg+"."+name,
						trace.String("invocation", n.Name),
						trace.String("mode", "denied"),
						trace.String("cause", unit.run.disposition.String()),
					)
				}
			}
			if unit.run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED {
				denied = append(denied, plannedSolos[unit.pkg]...)
			}
			for _, name := range denied {
				stopped, err := runFollowupAttempt(ctx, invCtx, n, unit.pkg, []string{name}, name, false, spawn, gate, leg, unit.run.execution, unit)
				if stopped || err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	res := &SelectionResult{}
	for _, u := range units {
		res.absorb(u)
	}
	return res, nil
}

// absorb folds one package's unit into the result: the process's
// outcomes, then each solo re-run's, attributed to its own producing
// process.
func (res *SelectionResult) absorb(u packageUnit) {
	res.absorbRun(u.pkg, &u.run)
	for i := range u.solos {
		res.absorbRun(u.pkg, &u.solos[i])
	}
}

func (res *SelectionResult) absorbRun(pkg string, r *packageRun) {
	res.Tests = append(res.Tests, r.tests...)
	res.Diagnostics = append(res.Diagnostics, r.diags...)
	if r.obs != nil {
		res.Observations = append(res.Observations, r.obs)
	}
	if r.timing != nil {
		res.Timing = append(res.Timing, r.timing)
	}
	res.Processes = append(res.Processes, ProcessOutcome{
		Package: pkg, Test: r.soloTest, Disposition: r.disposition, Producer: r.producer,
	})
}

// deniedTests derives the top-level runnables one selective process
// denied an outcome. Only a red process — its suite failed, its stream
// is untrusted, or its binary deadline expired — denies anything: a
// test shadowed by a package abort (started but unfinished, or selected
// yet reaching no terminal event at all) and a completed pass inside
// the process, whose green evidence the process's own red disposition
// voids. A test whose own top-level failure is recorded is never denied
// — its failure stands. A build failure would fail a solo re-run
// identically, so it isolates nothing. A TIMEOUT process isolates: a
// binary-deadline package leaves the envelope's budget intact, and each
// solo re-run gets a fresh binary bound of its own — one victim's
// starvation never voids its siblings' evidence — while under a
// spent envelope the loop denies each solo before it spawns, reported
// as a TIMEOUT process outcome exactly as the surface documents. A
// package whose process never ran is never asked: it has no outcome to
// isolate, and its witnesses' cause is the package's disposition.
func deniedTests(r *packageRun, selected []string) []string {
	switch r.disposition {
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT:
	default:
		return nil
	}
	denied := map[string]bool{}
	for _, name := range r.aborted {
		denied[topLevel(name)] = true
	}
	terminal := map[string]bool{}
	failed := map[string]bool{}
	for _, tr := range r.tests {
		name := tr.GetTest()
		if name != topLevel(name) {
			// Subtest and committed-seed outcomes ride their top-level
			// parent; only top-level runnables are selectable solo.
			continue
		}
		terminal[name] = true
		switch tr.GetOutcome() {
		case stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED:
			denied[name] = true
		case stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED:
			failed[name] = true
		}
	}
	for _, name := range selected {
		if !terminal[name] {
			denied[name] = true
		}
	}
	for name := range failed {
		delete(denied, name)
	}
	if len(denied) == 0 {
		return nil
	}
	out := make([]string, 0, len(denied))
	for name := range denied {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
