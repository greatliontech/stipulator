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
	"slices"
	"sort"
	"strings"

	"github.com/greatliontech/gofresh"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
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
//
// A non-empty scopeIds selects the scoped witness-evidence class:
// fresh records still serve for the whole tree, only stale subjects
// bound to the named requirements execute, requirements red solely on
// that boundary are classed scope-blocked and excluded from the
// verdict, prune residue is not derived, and the result names the
// scope. Unknown identifiers refuse. Scoping composes with the default
// class only - full demands the whole policy by definition.
func Run(ctx context.Context, dir string, full bool, scopeIds []string) (*stipulatorv1.CheckResult, error) {
	// The entry guard keeps every verdict short circuit — compile problems
	// and policy problems included — behind a live context: a cancelled
	// run aborts before it can render any partial judgment.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if full && len(scopeIds) > 0 {
		return nil, errors.New("ids scoping composes with the default witness-evidence class only - suite judgment executes the whole policy by definition")
	}
	res := &stipulatorv1.CheckResult{}
	fsys := os.DirFS(dir)

	// Phase marks feed the operation's progress seam; with no reporter
	// installed (the CLI path) every mark is a no-op.
	rep := progress.FromContext(ctx)
	rep.Phase(stipulatorv1.Phase_PHASE_COMPILE)
	// Everything the held inputs decide — compile errors, the manifest's
	// coverage policy, the records' hygiene — is judged here, before the
	// accepted policy is captured and before any child process
	// (REQ-check-preparation).
	prepared, err := Prepare(fsys)
	if err != nil {
		return nil, err
	}
	// Re-checked after the pass's first long stage so a cancellation
	// during compilation aborts before the compile-problems verdict, not
	// after it.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if problems := prepared.CompileProblems(); len(problems) > 0 {
		res.SetCompileProblems(problems)
		return res, nil
	}
	spec, store, covPol := prepared.Spec, prepared.Store, prepared.Coverage
	// The caller's identifier vocabulary is judged at parse: unknown
	// identifiers refuse before any evidence is gathered.
	var scope map[gofresh.Subject]bool
	if len(scopeIds) > 0 {
		// The scope is a set: the result names each id once, in one
		// order, however the caller spelled the list.
		scopeIds = slices.Compact(slices.Sorted(slices.Values(scopeIds)))
		if scope, err = ScopeSubjects(spec, store, scopeIds); err != nil {
			return nil, err
		}
	}

	// The policy is explicit, never assumed: witness execution consumes the
	// committed record, so a missing or invalid record is a failing check
	// with the loader's guidance, not a silent fallback to some universal
	// invocation. A record problem is the check's verdict, whether the
	// loader found it or a later reader did — an invocation whose
	// selection this tree cannot resolve is the record's fault against
	// the tree (REQ-policy-explicit); an operational fault — a permission
	// error, not a record problem — stays an error: it says nothing about
	// the tree.
	recordProblem := func(err error) (*stipulatorv1.CheckResult, error) {
		if !errors.Is(err, policy.ErrRecord) {
			return nil, err
		}
		p := &stipulatorv1.Problem{}
		p.SetPath(policy.Path)
		p.SetMessage(err.Error())
		res.SetPolicyProblem(p)
		return res, nil
	}

	// Records that fail hygiene fail verification whatever a witness run
	// would say, so the pass takes its witness-free form: no policy
	// capture, no child process, and the verdict — verification problems
	// — rendered from the records alone (REQ-check-preparation).
	var testRun *verify.TestRun
	var report *stipulatorv1.ExecutionReport
	var backends map[string]verify.Backend
	if len(prepared.Hygiene) == 0 {
		// The load is the operation's one capture of the accepted
		// policy: every invocation normalized once here, and every
		// reader below — the notices, selection, execution, the outside
		// accounting — consults it (REQ-check-derivation). A
		// normalization fault is the check's fault, before the
		// verification backend opens a child.
		pc, err := golang.LoadCapture(ctx, dir)
		if err != nil {
			return recordProblem(err)
		}

		// Policy-tier notices surface at load, attributed to the
		// invocation that authored the condition — a degradation must be
		// visible where it was declared, not only mid-derivation on an
		// engine's diagnostic face. Advisory: never a verdict input.
		res.SetPolicyNotices(golang.SelectionNotices(pc))

		// The verification backend is prepared before witnessing over
		// the operation's whole symbol set: resolutions and witness
		// classifications proven fresh serve from records, and the
		// owned child opens only for the stale remainder
		// (REQ-evidence-resolution-freshness); the witness run's
		// classifier and the binding resolution share it.
		symbols, err := golang.OperationSymbols(ctx, store, pc)
		if err != nil {
			return recordProblem(err)
		}
		gb, err := golang.NewServed(ctx, dir, symbols)
		if err != nil {
			return nil, err
		}
		defer gb.Close()
		backends = map[string]verify.Backend{"go": gb}
		// The serving path's account rides the result: what served,
		// what resolved typed and why, and any selection the path
		// degraded (REQ-evidence-freshness-degrade) — advisory, never a
		// verdict input.
		defer func() { res.SetResolutionNotices(gb.Notices()) }()

		// The evidence-class fork (REQ-check-verdict): health judgment
		// demands whole-policy execution, so the full form executes
		// everything and the default form serves proven-fresh witnesses
		// with witness-only selective execution of the stale remainder —
		// claiming no health.
		if full {
			report, testRun, err = golang.ExecutePolicyWitnessed(ctx, pc, gb)
			if err != nil {
				return recordProblem(err)
			}
			res.SetExecution(report)
			res.SetSuiteHealthJudged(true)
		} else if scope != nil {
			testRun, err = golang.RunWitnessesScoped(ctx, pc, scope, gb)
			if err != nil {
				return recordProblem(err)
			}
			res.SetScopePartial(true)
			res.SetScopeIds(append([]string(nil), scopeIds...))
			res.SetTestsServed(int32(testRun.Fresh))
			res.SetWitnessDiagnostics(testRun.Diagnostics)
		} else {
			testRun, err = golang.RunWitnessesPolicy(ctx, pc, gb)
			if err != nil {
				return recordProblem(err)
			}
			res.SetTestsServed(int32(testRun.Fresh))
			// No execution report exists to carry retained failure output
			// on this form, so the typed diagnostics ride the result
			// directly — disposition and truncation intact
			// (REQ-check-diagnostics).
			res.SetWitnessDiagnostics(testRun.Diagnostics)
		}
		res.SetTestsExecuted(int32(testRun.Ran))
		res.SetTestsOutsidePolicy(int32(testRun.OutsidePolicy))
		// The catastrophic shape - nothing served and no witness outcome
		// granted while expected witnesses sit outside the eligible selection
		// - names its execution-layer cause once at result level; without it
		// every affected binding reads as a tree defect (its per-binding
		// reason still carries the class). Keying on granted outcomes
		// rather than recorded ones keeps non-race legs - which run, and
		// whose failures and skips are recorded, but which can never grant
		// - from masking the cause; a pass is recorded only where a grant
		// is made, on both evidence forms.
		if testRun.Fresh == 0 && testRun.Granted() == 0 && testRun.OutsidePolicy > 0 {
			res.SetWitnessSelectionProblem(fmt.Sprintf("the witness-eligible selection covered no expected witness: %d expected witnesses are outside it - witness evidence derives only from race: true invocations or explicit plain_witness: true admissions", testRun.OutsidePolicy))
		}
		res.SetTestsUncacheable(int32(testRun.Uncached))
		res.SetUncacheableReasons(testRun.UncacheableReasons)
		res.SetExecutedReasons(testRun.ExecutedReasons)
		res.SetWitnessPublicationDegraded(testRun.Degraded)
	} else if _, _, err := policy.Load(dir, map[string]policy.Backend{"go": golang.Policy{}}); err != nil {
		// The policy term still stands on the witness-free pass: the
		// record's static faults decide without a toolchain query
		// (REQ-check-verdict). What the pass forgoes is the capture —
		// the notices and the tree-resolved faults it would cost.
		if !errors.Is(err, policy.ErrRecord) {
			return nil, err
		}
		p := &stipulatorv1.Problem{}
		p.SetPath(policy.Path)
		p.SetMessage(err.Error())
		res.SetPolicyProblem(p)
	}
	rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	vr := verify.Run(spec, store, backends, testRun)
	vp := vr.Proto()
	// The typed failure rows already ride the check payload — at the
	// check level on the witness-evidence form, on the execution report
	// when health is judged — so the verify sub-message leaves its rows
	// empty: one fact, one home per payload
	// (REQ-mcp-response-contract).
	vp.SetWitnessDiagnostics(nil)
	res.SetVerify(vp)

	rep.Phase(stipulatorv1.Phase_PHASE_COVERAGE)
	cov := coverage.Evaluate(spec, vr, store, testRun != nil, covPol)
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
	if len(scopeIds) == 0 && testRun != nil {
		for _, g := range cov.Gaps {
			if g.State == coverage.Resolved {
				residue = append(residue, g.Path)
			}
		}
		res.SetPruneResidue(residue)
	}
	// A scoped pass derives no residue: resolved-gap evidence takes the
	// serving class over the whole tree (REQ-gap-resolved-pruned), which
	// a scope deliberately does not provide.

	// The witness-evidence form omits the health term: it demanded no
	// suite-health disposition, so health can neither pass nor fail it.
	healthy := true
	if full {
		healthy = golang.SuiteHealthy(report)
	}
	// Observed red is a verdict input on every form: an execution this
	// run performed and watched dispose unhealthy — a failed test, a
	// degraded, build-failed, or timed-out process — fails the tree
	// whatever the failing test is bound to. The failure diagnostics are
	// exactly those dispositions (REQ-check-diagnostics), homed on the
	// result for the witness-evidence forms and on the execution report
	// when health is judged; what the default form declines to claim is
	// health over what it did NOT execute, never a pass over a red it
	// saw (REQ-check-verdict).
	observedRed := len(res.GetWitnessDiagnostics()) > 0 || len(report.GetDiagnostics()) > 0
	gatePasses := cov.GatePasses()
	if len(scopeIds) > 0 {
		// The scoped verdict excludes rows red solely on the scope
		// boundary: they were deliberately not executed, and the result
		// is flagged partial so it is never mistaken for a global one.
		gatePasses = scopedGatePasses(cov)
	}
	res.SetPassed(len(vr.Problems) == 0 &&
		res.GetPolicyProblem() == nil &&
		healthy &&
		!observedRed &&
		gatePasses &&
		len(residue) == 0)
	return res, nil
}

// ScopeSubjects resolves the named requirement identifiers to the
// witness subjects their tests- and proves-role bindings name - the one
// derivation every id-scoped witness evaluation shares (the scoped
// check, prune's gap-scoped resolution detection). Unknown identifiers
// refuse - a typo must not silently produce an empty scope that
// executes nothing and passes; callers whose id sets legitimately carry
// out-of-corpus entries filter them first.
func ScopeSubjects(spec *stipulatorv1.Spec, store *records.Store, ids []string) (map[gofresh.Subject]bool, error) {
	known := records.HashesOf(spec)
	scope := map[gofresh.Subject]bool{}
	want := map[string]bool{}
	for _, id := range ids {
		if !known.Known(id) {
			return nil, fmt.Errorf("unknown requirement identifier %q in ids scope", id)
		}
		want[id] = true
	}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if !want[b.GetRequirementId()] {
				continue
			}
			role := b.GetRole()
			if role != stipulatorv1.BindingRole_BINDING_ROLE_TESTS && role != stipulatorv1.BindingRole_BINDING_ROLE_PROVES {
				continue
			}
			// A bound witness symbol is "<import-path>.<TestName>": the
			// subject splits at the last dot, matching the witness run's
			// expected-set keying.
			sym := b.GetSymbol()
			if i := strings.LastIndex(sym, "."); i > 0 {
				scope[gofresh.Subject{Package: sym[:i], Symbol: sym[i+1:]}] = true
			}
		}
	}
	return scope, nil
}

// GapScope derives the requirements a gap evaluation reads - the
// gap-named ids plus covered(<id>) landing-condition targets, filtered
// to the corpus (an out-of-corpus id is dangling: never resolvable,
// owned by the dangling surfaces) - and resolves them to their bound
// witness subjects. The one derivation prune and the gap list share.
func GapScope(spec *stipulatorv1.Spec, store *records.Store) (map[gofresh.Subject]bool, []string, error) {
	known := records.HashesOf(spec)
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if known.Known(id) && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, g := range store.Gaps {
		add(g.Gap.GetRequirementId())
		if c := g.Gap.GetLands().GetCovered(); c != "" {
			add(c)
		}
	}
	sort.Strings(ids)
	scope, err := ScopeSubjects(spec, store, ids)
	if err != nil {
		return nil, nil, err
	}
	return scope, ids, nil
}

// scopedGatePasses is the scoped verdict's gate term: every violation
// whose row is red solely on the scope boundary is excluded.
func scopedGatePasses(cov *coverage.Report) bool {
	blocked := map[string]bool{}
	for _, r := range cov.Requirements {
		if r.ScopeBlocked {
			blocked[r.Id] = true
		}
	}
	for _, v := range cov.Violations {
		if !blocked[v] {
			return false
		}
	}
	return true
}
