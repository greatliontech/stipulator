// Package check runs the unified check: one in-process evaluation pass —
// compilation, witness evidence, binding verification, coverage and gap
// evaluation, and prune residue — composed into one CheckResult carrying
// one verdict.
//
// Witness evidence has two forms (REQ-check-verdict). The default serves
// proven-fresh witness records and selectively executes only the stale
// remainder — a witness-evidence invocation that demands no suite-health
// disposition, per REQ-core-one-execution's witness-only class. The full
// form executes the accepted policy whole, so suite health and witness
// evidence derive from the same execution and a witness failure occurs
// inside the run whose health the verdict judged. Either way the pass
// composes in-process: every stage is a library call, never a subprocess
// invocation of the individual operations; child processes exist only
// behind the Go backend's owned execution seam. Every human rendering of
// the check is a projection of the returned message.
package check

import (
	"context"
	"errors"
	"fmt"
	"os"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Run executes the unified check over the corpus rooted at dir. Every
// judgment about the tree rides the returned CheckResult; the error
// return is reserved for operational faults — cancellation included: a
// cancelled run aborts cleanly with no partial verdict.
//
// By default the pass takes its witness evidence from freshness-served
// records plus witness-only selective execution of the stale remainder,
// claims no suite health, and fails exactly when compilation fails, the
// accepted test policy cannot load, verification reports problems, some
// red requirement has no gap naming it, or prune residue remains. With
// full set the accepted policy executes whole, health derives from that
// same execution, and the verdict additionally fails when suite health
// is unhealthy (REQ-check-verdict). A tree failing the check is a fact
// in the result, never an error.
func Run(ctx context.Context, dir string, full bool) (*stipulatorv1.CheckResult, error) {
	// The entry guard keeps every verdict short circuit — compile problems
	// and policy problems included — behind a live context: a cancelled
	// run aborts before it can render any partial judgment.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res := &stipulatorv1.CheckResult{}
	fsys := os.DirFS(dir)

	// Phase marks feed the operation's progress seam; with no reporter
	// installed (the CLI path) every mark is a no-op.
	rep := progress.FromContext(ctx)
	rep.Phase(stipulatorv1.Phase_PHASE_COMPILE)
	spec, diags, err := compile.Compile(fsys)
	if err != nil {
		return nil, err
	}
	// Re-checked after the pass's first long stage so a cancellation
	// during compilation aborts before the compile-problems verdict, not
	// after it.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if errs := compile.Errors(diags); len(errs) > 0 {
		problems := make([]*stipulatorv1.Problem, 0, len(errs))
		for _, d := range errs {
			p := &stipulatorv1.Problem{}
			p.SetPath(fmt.Sprintf("%s:%d", d.Document, d.Line))
			p.SetMessage(d.Message)
			problems = append(problems, p)
		}
		res.SetCompileProblems(problems)
		return res, nil
	}

	store, err := records.Load(fsys)
	if err != nil {
		return nil, err
	}

	// The policy is explicit, never assumed: witness execution consumes the
	// committed record, so a missing or invalid record is a failing check
	// with the loader's guidance, not a silent fallback to some universal
	// invocation. An operational fault reading the record — a permission
	// error, not a record problem — stays an error: it says nothing about
	// the tree.
	pol, _, err := policy.Load(dir, map[string]policy.Backend{"go": golang.Policy{}})
	if err != nil {
		if !errors.Is(err, policy.ErrRecord) {
			return nil, err
		}
		p := &stipulatorv1.Problem{}
		p.SetPath(policy.Path)
		p.SetMessage(err.Error())
		res.SetPolicyProblem(p)
		return res, nil
	}

	// The evidence-class fork (REQ-check-verdict): health judgment demands
	// whole-policy execution, so the full form executes everything and the
	// default form serves proven-fresh witnesses with witness-only
	// selective execution of the stale remainder — claiming no health.
	var testRun *verify.TestRun
	var report *stipulatorv1.ExecutionReport
	if full {
		report, testRun, err = golang.ExecutePolicyWitnessed(ctx, dir, pol)
		if err != nil {
			return nil, err
		}
		res.SetExecution(report)
		res.SetSuiteHealthJudged(true)
	} else {
		testRun, err = golang.RunWitnessesPolicy(ctx, dir, pol)
		if err != nil {
			return nil, err
		}
		res.SetTestsServed(int32(testRun.Fresh))
		// No execution report exists to carry retained failure output on
		// this form, so the typed diagnostics ride the result directly —
		// disposition and truncation intact (REQ-check-diagnostics).
		res.SetWitnessDiagnostics(testRun.Diagnostics)
	}
	res.SetTestsExecuted(int32(testRun.Ran))
	res.SetTestsUncacheable(int32(testRun.Uncached))
	res.SetWitnessPublicationDegraded(testRun.Degraded)

	gb, err := golang.NewOwned(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer gb.Close()
	backends := map[string]verify.Backend{"go": gb}
	rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	vr := verify.Run(spec, store, backends, testRun)
	res.SetVerify(vr.Proto())

	manifest, err := corpus.LoadManifest(fsys)
	if err != nil {
		return nil, err
	}
	covPol, err := coverage.PolicyFromManifest(manifest)
	if err != nil {
		return nil, err
	}
	rep.Phase(stipulatorv1.Phase_PHASE_COVERAGE)
	cov := coverage.Evaluate(spec, vr, store, true, covPol)
	res.SetCoverage(cov.Proto())

	// Prune residue is echoed from this same witnessed gap evaluation —
	// one source, so the residue and the coverage gap states cannot
	// diverge. Witnessing is what makes the residue complete: an
	// unwitnessed evaluation suppresses witness and proof evidence, so a
	// gap whose requirement resolves only through an executed witness is
	// structurally undetectable there; inside the check's single witnessed
	// pass the lingering record is visible the moment its requirement
	// reaches covered.
	var residue []string
	for _, g := range cov.Gaps {
		if g.State == coverage.Resolved {
			residue = append(residue, g.Path)
		}
	}
	res.SetPruneResidue(residue)

	// The witness-evidence form omits the health term: it demanded no
	// suite-health disposition, so health can neither pass nor fail it.
	healthy := true
	if full {
		healthy = golang.SuiteHealthy(report)
	}
	res.SetPassed(len(vr.Problems) == 0 &&
		healthy &&
		cov.GatePasses() &&
		len(residue) == 0)
	return res, nil
}
