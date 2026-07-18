package golang

import (
	"context"
	"fmt"
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// The selective executor is the witness-only execution surface: it runs a
// caller-chosen subset of one normalized invocation's packages, each
// narrowed to named top-level runnables, and grants witness evidence,
// never suite health (REQ-core-one-execution's witness-only selective
// execution). What it adds over plain package execution is the isolation
// pass: a selective process can deny its sibling tests an outcome — a
// package abort shadows tests that never reached a terminal event, and a
// red process yields no green evidence for the passes that completed
// inside it — so each denied test is re-run solo, one top-level runnable
// per process, once, inside the same invocation envelope. The isolated
// outcome is a real run's outcome from its own producing process
// (REQ-evidence-witness-freshness, REQ-policy-attribution); the denying
// process's own failures stand untouched.

// TestSelection narrows an execution to named top-level runnables per
// package: each key is a package import path, its value the top-level
// Test and Fuzz function names that package's process executes (a
// single-element Fuzz selection replays the target's committed seeds). A
// nil map — or a nil entry — selects the whole package.
type TestSelection map[string][]string

// ProcessOutcome is one process of a selective execution: which package
// it ran, the single runnable it isolated (empty for a package-selection
// process), its terminal disposition, and its producer identity — nil
// exactly when the envelope denied the process before it spawned.
type ProcessOutcome struct {
	Package     string
	Test        string
	Disposition stipulatorv1.HealthDisposition
	Producer    *stipulatorv1.ProducerIdentity
}

// SelectionResult is one witness-only selective execution's report:
// named test outcomes and failure diagnostics attributed to their
// producing processes, one owned observation per launched process, and
// every process's terminal disposition — package-selection processes
// first in package order, then solo isolation processes in re-run order.
// It deliberately carries no invocation health: a selective execution
// grants witness evidence and never any health (REQ-core-one-execution),
// so consumers key on per-process dispositions, not a suite verdict.
type SelectionResult struct {
	Tests        []*stipulatorv1.TestResult
	Diagnostics  []*stipulatorv1.FailureDiagnostic
	Observations []*ProcessObservation
	Processes    []ProcessOutcome
}

// ExecuteSelection executes a witness-only selection of one normalized
// invocation: the selected packages, each narrowed to its top-level
// runnables, one owned `go test -json` process per package under the
// invocation's reviewed envelope timeout, followed by the isolation pass
// — every test a red or aborted process denied an outcome is re-run
// solo, once, inside the same envelope context, so retries can never
// outlive the invocation's reviewed bound. Solo outcomes join the report
// attributed to their own producing process. Caller cancellation
// discards the partial run: the return is (nil, ctx.Err()), never a
// partial report (REQ-policy-cancellation). Envelope expiry is a
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
	pkgs := make([]string, 0, len(sel))
	for pkg := range sel {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("invocation %q: selection names no packages", n.Name)
	}
	// The envelope carries its identity as the context cause exactly as in
	// ExecuteInvocation: kill-path dump and grace only on true envelope
	// expiry, never on a caller's own deadline.
	invCtx, cancel := context.WithTimeoutCause(ctx, n.Timeout, errEnvelopeExpired)
	defer cancel()
	spawn := spawnOrdinals()
	runs := runSelectedPackages(ctx, invCtx, n, pkgs, sel, spawn)
	if err := ctx.Err(); err != nil {
		// Caller cancellation: the partial run is discarded whole.
		return nil, err
	}
	res := &SelectionResult{}
	for i := range runs {
		r := &runs[i]
		if err := finalizeRun(n, r, invCtx.Err() != nil, ""); err != nil {
			return nil, err
		}
		res.Tests = append(res.Tests, r.tests...)
		res.Diagnostics = append(res.Diagnostics, r.diags...)
		if r.obs != nil {
			res.Observations = append(res.Observations, r.obs)
		}
		res.Processes = append(res.Processes, ProcessOutcome{
			Package: r.pkg, Disposition: r.disposition, Producer: r.producer,
		})
	}
	// The isolation pass runs sequentially under the same envelope
	// context: the envelope bounds retries, so an expired envelope denies
	// a re-run before it spawns — reported as a TIMEOUT process outcome —
	// and each denied test is re-run exactly once, never recursively.
	for i := range runs {
		r := &runs[i]
		for _, name := range deniedTests(r, sel[r.pkg]) {
			solo := packageRun{pkg: r.pkg}
			if invCtx.Err() == nil {
				solo = runPackage(invCtx, n, r.pkg, []string{name}, spawn())
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := finalizeRun(n, &solo, invCtx.Err() != nil, name); err != nil {
				return nil, err
			}
			res.Tests = append(res.Tests, solo.tests...)
			res.Diagnostics = append(res.Diagnostics, solo.diags...)
			if solo.obs != nil {
				res.Observations = append(res.Observations, solo.obs)
			}
			res.Processes = append(res.Processes, ProcessOutcome{
				Package: r.pkg, Test: name, Disposition: solo.disposition, Producer: solo.producer,
			})
		}
	}
	return res, nil
}

// deniedTests derives the top-level runnables one selective process
// denied an outcome. Only a red process — its suite failed, or its
// stream is untrusted — denies anything: a test shadowed by a package
// abort (started but unfinished, or selected yet reaching no terminal
// event at all) and a completed pass inside the process, whose green
// evidence the process's own red disposition voids. A test whose own
// top-level failure is recorded is never denied — its failure stands. A
// build failure or envelope expiry would deny a solo re-run identically,
// so those dispositions isolate nothing.
func deniedTests(r *packageRun, selected []string) []string {
	switch r.disposition {
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED:
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
