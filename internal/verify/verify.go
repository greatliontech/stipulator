// Package verify checks the committed records against the compiled corpus
// and, through language backends, against the code.
//
// Problems are verification errors — malformed or dangling records,
// unloadable trees, claims against generated files — and always fail a
// run. Everything else is reported as per-binding data (resolution
// outcome, pin and shape state): those facts feed the coverage buckets,
// where gap records may excuse them, so this layer never hard-fails on
// them. The package defines the Backend interface but depends on no
// backend implementation.
package verify

import (
	"errors"
	"fmt"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
)

// Problem is a record inconsistency; any problem fails verification.
type Problem struct {
	Path    string
	Message string
}

func (p Problem) String() string { return p.Path + ": " + p.Message }

// Resolution classifies a backend's answer for a symbol reference.
type Resolution int

const (
	// Unverified: no backend answer for the binding — no backend
	// registered, a resolve that faulted (its problem reported beside),
	// or a served backend answering that it did not verify.
	Unverified Resolution = iota
	// Resolved: the symbol exists; its shape hash accompanies it.
	Resolved
	// NotFound: the symbol does not exist — the binding is broken.
	NotFound
	// GeneratedFile: the symbol lives in a generated file; the claim
	// belongs on the generating artifact.
	GeneratedFile
)

// ShapeState classifies a resolved binding's shape pin.
type ShapeState int

const (
	// ShapeUnknown: the binding did not resolve, so no shape comparison
	// happened.
	ShapeUnknown ShapeState = iota
	// ShapeUnpinned: no shape hash recorded yet — stale, awaiting pin.
	ShapeUnpinned
	// ShapeMatch: the pinned shape equals the symbol's current shape.
	ShapeMatch
	// ShapeMismatch: the symbol's shape moved — the binding is broken
	// until re-verified and re-pinned.
	ShapeMismatch
)

// TestOutcome is the witnessed outcome of a bound test.
type TestOutcome int

const (
	// TestNotRun: no outcome observed — the run had no witnessing, the
	// test was filtered out, or its package failed to build.
	TestNotRun TestOutcome = iota
	TestPassed
	TestFailed
	TestSkipped
)

// TestRun carries the observed outcomes of one test execution: the raw
// material witnesses are derived from. Producing it is backend work;
// correlating it is this package's.
type TestRun struct {
	// RaceEnabled records whether the run's witness grants derive from
	// race-enabled invocations: the rigor attribute a witness inherits
	// unless PlainWitness marks its key downgraded.
	RaceEnabled bool
	// PlainWitness marks, by "<import-path>.<TestName>", outcomes granted
	// exclusively by an explicit plain-witness admission: the recorded
	// tier downgrade (REQ-check-witness-selection).
	PlainWitness map[string]bool
	// Outcomes maps "<import-path>.<TestName>[/<subtest>...]" to the
	// observed outcome.
	Outcomes map[string]TestOutcome
	// Registrations are runtime coverage claims emitted through the
	// stipulate marker, in deterministic order.
	Registrations []Registration
	// Uncached counts executed tests whose records could not be
	// published for reuse (unverifiable dependences): they will run
	// again next time. A silently shrinking cache reads as "covered";
	// the count keeps the cost visible.
	Uncached int
	// Ran and Fresh count top-level tests executed vs served from the
	// witness cache by proven equivalence (REQ-evidence-witness-freshness).
	Ran   int
	Fresh int
	// Degraded carries the freshness-path fault when the run fell back to
	// the full suite (REQ-evidence-freshness-degrade); empty on the
	// freshness path proper.
	Degraded string
	// OutsideSubjects marks, by "<import-path>.<TestName>", each expected
	// witness subject the accepted policy's witness-eligible selection
	// does not cover - witness evidence derives from race invocations, so
	// these can never witness until the policy covers them.
	OutsideSubjects map[string]bool
	// ScopeSkipped marks, by "<import-path>.<TestName>", each stale
	// subject a caller-named id scope left unexecuted: its bindings read
	// scope-skipped, never broken (REQ-check-verdict's scoped class).
	ScopeSkipped map[string]bool
	// NoOutcome names, by "<import-path>.<TestName>", the execution-layer
	// cause for each expected subject the witness-eligible selection
	// covers but the run granted nothing — no result, or a result its
	// package's process denied a grant: the producing invocation and the
	// package's disposition (a timeout, a build failure) — so its
	// bindings read broken for that cause, never as outside the
	// selection (REQ-check-witness-selection).
	NoOutcome map[string]string
	// SelectiveServing marks the run's execution class: true when the
	// selective witness runner produced it — proven-fresh records served
	// with selective execution of the stale remainder, the degraded
	// empty-served form included — false for one whole policy execution.
	// Consumers the spec pins to the serving class
	// (REQ-gap-resolved-pruned) refuse a run without the mark.
	SelectiveServing bool
	// OutsidePolicy counts expected witness subjects the accepted test
	// policy leaves outside selective witnessing — subjects whose package
	// no invocation covers, only a non-race invocation covers, or more
	// than one invocation covers (REQ-core-one-execution: such subjects
	// neither serve nor execute). The count keeps the gap visible in
	// reports and views rather than silent.
	OutsidePolicy int
	// Failures carries each failed top-level test's output tail, keyed like
	// Outcomes: a red witness must be diagnosable from the run that saw it,
	// not by re-running the suite by hand.
	Failures map[string]string
	// Diagnostics carries the same failures as typed rows — disposition,
	// truncation, and retained output intact — for consumers that must
	// name a degraded execution distinctly from an assertion failure
	// (REQ-check-diagnostics) where no execution report exists.
	Diagnostics []*stipulatorv1.FailureDiagnostic
	// ExecutedReasons names, per re-executed top-level test that held
	// prior witness evidence, why serving refused it - the stale
	// variant's verdict reason with gofresh's movers named. Cold
	// subjects (no prior record) are absent: their cause is the absence
	// itself (REQ-evidence-witness-freshness).
	ExecutedReasons map[string]string
	// UncacheableReasons names, per executed top-level test that could
	// not publish, the leg that refused — the sealed observation's own
	// reason, the refused proof's, the missing granting process, the
	// post-run drift with its moved inputs — so the uncacheable count is
	// a diagnosable set, never a bare number
	// (REQ-evidence-witness-freshness).
	UncacheableReasons map[string]string
}

// Registration is one runtime coverage claim. Package and the test path
// are carried separately: import paths contain slashes, so a fused string
// cannot be split back into its parts.
type Registration struct {
	// Package is the import path of the registering test's package.
	Package string
	// Test is the test path within the package, "TestName[/subtest...]".
	Test        string
	Requirement string
}

// TopLevel returns the top-level test function name of the registration.
func (r Registration) TopLevel() string {
	if i := strings.Index(r.Test, "/"); i >= 0 {
		return r.Test[:i]
	}
	return r.Test
}

// RegistrationResult is a cross-checked registration.
type RegistrationResult struct {
	Registration
	Outcome TestOutcome
}

// BindingResult is the verified state of one binding: the facts the
// coverage buckets are computed from.
type BindingResult struct {
	Path          string
	RequirementId string
	Symbol        string
	// Clause is the payload clause the claim is scoped to, resolved
	// against the current corpus; nil for a whole-requirement claim
	// (REQ-evidence-clause-claim). A claim naming a clause the
	// requirement no longer declares is malformed and never reaches a
	// result.
	Clause *stipulatorv1.Clause
	// Package is the symbol's owning package as the backend resolved it
	// (SymbolLocator); empty when no backend answer exists.
	Package string
	Backend string
	Role    stipulatorv1.BindingRole
	// ContentPinned reports whether the record's consent to the
	// requirement's text holds: the content pin equals the current hash,
	// or the source pin equals the current consent-source digest
	// (REQ-evidence-consent-current).
	ContentPinned bool
	// Rehash marks a consent that holds by the source pin alone: the
	// canonical form moved over byte-identical text, and the content
	// pin awaits the blanket pin's rewrite.
	Rehash     bool
	Resolution Resolution
	Shape      ShapeState
	// TestOutcome is set for tests- and proves-role bindings when the run
	// witnessed tests; WitnessClass and RaceEnabled qualify the witness.
	TestOutcome  TestOutcome
	WitnessClass WitnessClass
	// WitnessClassReason names, for an example classification, what the
	// bound body lacks - surfaced on uncovered rows so a misclassified
	// witness is diagnosed from the output.
	WitnessClassReason string
	RaceEnabled        bool
	// OutsideWitnessSelection marks a tests- or proves-role binding whose
	// subject the accepted policy's witness-eligible selection does not
	// cover: it cannot witness until the policy covers it, and its
	// TestNotRun outcome names that class rather than a tree defect.
	OutsideWitnessSelection bool
	// NoOutcomeCause names, for a tests- or proves-role binding the
	// eligible selection covers but the run granted nothing, the
	// producing invocation and its package's disposition
	// (REQ-check-witness-selection); empty otherwise.
	NoOutcomeCause string
	// ScopeSkipped marks a tests- or proves-role binding whose stale
	// subject the caller's id scope left unexecuted.
	ScopeSkipped bool
}

// witnessRole reports whether a binding's role carries a test outcome:
// tests and proves both name executable symbols.
func witnessRole(role stipulatorv1.BindingRole) bool {
	return role == stipulatorv1.BindingRole_BINDING_ROLE_TESTS ||
		role == stipulatorv1.BindingRole_BINDING_ROLE_PROVES
}

// ServingClassRequired refuses witness evidence that is not the serving
// class: proven-fresh records with selective execution of the stale
// remainder, never one whole policy execution demanded for the caller's
// operation alone (REQ-gap-resolved-pruned). A nil run carries no
// witness evidence and passes — the caller declared no-test semantics.
func ServingClassRequired(tr *TestRun) error {
	if tr != nil && !tr.SelectiveServing {
		return ErrNotServingClass
	}
	return nil
}

// ErrNotServingClass names the refused execution class.
var ErrNotServingClass = errors.New("this operation takes serving-class witness evidence (proven-fresh records with selective execution of the stale remainder), never a whole policy execution")

// Report is the outcome of a verification run.
type Report struct {
	Problems []Problem
	// Results holds the verified state of every well-formed binding, in
	// store order.
	Results []BindingResult
	// Pinned counts bindings whose consent holds; Stale counts bindings
	// whose consent does not (an unset or differing content pin with no
	// matching source pin). Rehash counts consent RECORDS — bindings
	// and standing attestations alike — held by the source pin alone:
	// current, awaiting the blanket pin's rewrite
	// (REQ-evidence-consent-current).
	Pinned, Stale, Rehash int
	// Witnessed records whether the run executed (or served) tests:
	// the witness counters mean nothing on an unwitnessed report.
	Witnessed bool
	// ShapePinned, ShapeUnpinned, and ShapeMismatch count resolved
	// bindings by shape-pin state; Broken counts bindings whose symbol
	// did not resolve; Unverified counts bindings with no backend
	// answer in this run — no backend registered, a resolve that
	// faulted (its problem is reported beside), or a served backend
	// answering that it did not verify.
	ShapePinned, ShapeUnpinned, ShapeMismatch, Broken, Unverified int
	// Registrations holds the cross-checked runtime coverage claims;
	// TestsPassed, TestsFailed, and TestsNotRun count tests- and
	// proves-role bindings by witnessed outcome (TestsNotRun counts bound
	// tests that produced no outcome in a witnessed run — unwitnessed,
	// reads as broken).
	Registrations                         []RegistrationResult
	TestsPassed, TestsFailed, TestsNotRun int
	// OutsidePolicy counts expected witness subjects the accepted test
	// policy left outside selective witnessing in the witnessed run
	// (REQ-core-one-execution: such subjects neither serve nor execute);
	// carried from the test run so every report surface renders the gap
	// as a visible number, never silence. Zero in unwitnessed runs.
	OutsidePolicy int
	// Diagnostics carries the witnessed run's failure diagnostics as
	// typed rows — test- and package-scoped alike, disposition and
	// truncation intact — the one home for retained failure output in a
	// verify report: a subject denied an outcome is diagnosable from the
	// same report that says so. Nil in unwitnessed runs.
	Diagnostics []*stipulatorv1.FailureDiagnostic
	// Attestations holds the verified state of every well-formed
	// requirement attestation, in store order.
	Attestations []AttestationResult
	// Signatures classify each changed requirement's shape against the
	// record pins — the baseline; no verification outcome is persisted
	// (REQ-gate-change-signature). Derived only in witnessed runs, in
	// requirement order.
	Signatures []ChangeSignature
}

// AttestationResult is one requirement attestation checked against the
// current corpus: the reason rides to coverage, and a stale content pin
// means the requirement moved since it was vouched for.
type AttestationResult struct {
	RequirementId string
	Reason        string
	ContentPinned bool
	// Rehash: the consent holds by the source pin alone
	// (REQ-evidence-consent-current).
	Rehash bool
}

// Run checks the store against the compiled spec, resolving symbols
// through the supplied backends (keyed by backend name; nil skips all
// symbol resolution) and correlating test outcomes from testRun (nil
// skips witnessing: role-tests bindings read TestNotRun).
func Run(spec *stipulatorv1.Spec, store *records.Store, backends map[string]Backend, testRun *TestRun) *Report {
	judge := newHygiene(spec, store)
	rep := &Report{}
	problem := func(path, format string, args ...any) {
		rep.Problems = append(rep.Problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}

	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			problems, malformed, clause := judge.binding(bf.Path, b)
			rep.Problems = append(rep.Problems, problems...)
			if malformed {
				continue
			}
			id := b.GetRequirementId()
			consent := judge.hashes.Judge(id, b.GetContentHash(), b.GetSourceHash())

			result := BindingResult{
				Path:          bf.Path,
				RequirementId: id,
				Symbol:        b.GetSymbol(),
				Clause:        clause,
				Backend:       b.GetBackend(),
				Role:          b.GetRole(),
				ContentPinned: consent.Holds(),
				Rehash:        consent == records.Rehash,
				Resolution:    Unverified,
				Shape:         ShapeUnknown,
			}

			if sl, ok := backends[b.GetBackend()].(SymbolLocator); ok {
				// A locator fault leaves the row package-less: scoped
				// diagnostic correlation then drops the row's package,
				// never mismatches it — and the same faulted backend
				// surfaces the run-level error through Resolve below.
				if pkg, err := sl.SymbolPackage(b.GetSymbol()); err == nil {
					result.Package = pkg
				}
			}

			if testRun != nil && witnessRole(b.GetRole()) {
				result.TestOutcome = testRun.Outcomes[b.GetSymbol()]
				result.OutsideWitnessSelection = testRun.OutsideSubjects[b.GetSymbol()]
				result.NoOutcomeCause = testRun.NoOutcome[b.GetSymbol()]
				result.ScopeSkipped = testRun.ScopeSkipped[b.GetSymbol()]
				// RaceEnabled qualifies a witness; a row without a passing
				// outcome carries no witness to qualify, so it never claims
				// the run's rigor for an outcome another invocation (or no
				// execution at all) produced.
				result.RaceEnabled = testRun.RaceEnabled && result.TestOutcome == TestPassed && !testRun.PlainWitness[b.GetSymbol()]
				if wc, ok := backends[b.GetBackend()].(WitnessClassVerdicts); ok {
					result.WitnessClass, result.WitnessClassReason = wc.WitnessClassVerdict(b.GetSymbol())
				} else if wc, ok := backends[b.GetBackend()].(WitnessClassifier); ok {
					result.WitnessClass = wc.WitnessClass(b.GetSymbol())
				}
			}

			if backend, ok := backends[b.GetBackend()]; ok {
				res, shape, err := backend.Resolve(b.GetSymbol())
				switch {
				case err != nil:
					problem(bf.Path, "resolving %s: %v", b.GetSymbol(), err)
				case res == GeneratedFile:
					// Rejection is a hard rule, never a bucket state.
					result.Resolution = GeneratedFile
					problem(bf.Path, "symbol %s is declared in a generated file; bind the generating artifact instead", b.GetSymbol())
				case res == NotFound:
					result.Resolution = NotFound
				case res == Unverified:
					// A served backend can answer that it did not
					// verify (an out-of-process resolver with no
					// verifier loaded): the row stays unverified — never
					// resolved with a shape verdict it did not compute.
					result.Resolution = Unverified
				default:
					result.Resolution = Resolved
					switch {
					case b.GetShapeHash() == "":
						result.Shape = ShapeUnpinned
					case b.GetShapeHash() == shape:
						result.Shape = ShapeMatch
					default:
						result.Shape = ShapeMismatch
					}
				}
			}
			rep.Results = append(rep.Results, result)
		}
	}

	if testRun != nil {
		rep.OutsidePolicy = testRun.OutsidePolicy
		rep.Diagnostics = testRun.Diagnostics
		// Cross-check runtime registrations: every registration must be
		// backed by a witness-role binding (tests or proves) for the same
		// requirement on the registration's top-level test — the binding
		// store remains the only claim source.
		type reqTest struct{ req, symbol string }
		backed := map[reqTest]bool{}
		for _, bf := range store.Bindings {
			for _, b := range bf.Set.GetBindings() {
				if witnessRole(b.GetRole()) {
					backed[reqTest{b.GetRequirementId(), b.GetSymbol()}] = true
				}
			}
		}
		for _, reg := range testRun.Registrations {
			symbol := reg.Package + "." + reg.TopLevel()
			if !backed[reqTest{reg.Requirement, symbol}] {
				problem("test run", "registration %s.%s covers %s, but no tests- or proves-role binding backs it", reg.Package, reg.Test, reg.Requirement)
				continue
			}
			rep.Registrations = append(rep.Registrations, RegistrationResult{
				Registration: reg,
				Outcome:      testRun.Outcomes[reg.Package+"."+reg.Test],
			})
		}
	}

	for _, af := range store.Attestations {
		for _, a := range af.Set.GetAttestations() {
			problems, stands := judge.attestation(af.Path, a)
			rep.Problems = append(rep.Problems, problems...)
			if !stands {
				continue
			}
			id := a.GetRequirementId()
			consent := judge.hashes.Judge(id, a.GetContentHash(), a.GetSourceHash())
			rep.Attestations = append(rep.Attestations, AttestationResult{
				RequirementId: id,
				Reason:        a.GetReason(),
				ContentPinned: consent.Holds(),
				Rehash:        consent == records.Rehash,
			})
		}
	}

	for _, gf := range store.Gaps {
		rep.Problems = append(rep.Problems, judge.gap(gf.Path, gf.Gap)...)
	}

	sortProblems(rep.Problems)
	rep.Witnessed = testRun != nil
	if rep.Witnessed {
		rep.Signatures = signatures(rep.Results)
	}
	rep.Tally()
	return rep
}

// Tally derives the report's counters from its rows — the one
// derivation, so a report sliced to a scope re-tallies the same way
// the whole tree was tallied (REQ-mcp-views: a scope narrows the whole
// report). Witness outcomes count only on a witnessed report: an
// unwitnessed row's zero outcome is not a test that never ran.
func (r *Report) Tally() {
	r.Pinned, r.Stale, r.Rehash = 0, 0, 0
	r.ShapePinned, r.ShapeUnpinned, r.ShapeMismatch, r.Broken, r.Unverified = 0, 0, 0, 0, 0
	r.TestsPassed, r.TestsFailed, r.TestsNotRun = 0, 0, 0
	for _, br := range r.Results {
		if br.ContentPinned {
			r.Pinned++
		} else {
			r.Stale++
		}
		if br.Rehash {
			r.Rehash++
		}
		if r.Witnessed && witnessRole(br.Role) {
			switch br.TestOutcome {
			case TestPassed:
				r.TestsPassed++
			case TestFailed:
				r.TestsFailed++
			case TestNotRun:
				// No outcome in a witnessed run: the test never ran
				// (package build failure, sibling panic aborting the
				// binary) — unwitnessed, reads as broken.
				r.TestsNotRun++
			}
		}
		switch br.Resolution {
		case Unverified:
			r.Unverified++
		case NotFound:
			r.Broken++
		case Resolved:
			switch br.Shape {
			case ShapeUnpinned:
				r.ShapeUnpinned++
			case ShapeMatch:
				r.ShapePinned++
			case ShapeMismatch:
				r.ShapeMismatch++
			}
		}
	}
	for _, a := range r.Attestations {
		if a.Rehash {
			r.Rehash++
		}
	}
}

// Granted counts the witness outcomes granted: the passes. A pass enters
// the outcome map only where a grant is made — a witness-eligible leg
// with a healthy disposition, or a served record — on both evidence
// forms, so the passes ARE the grant set and no second record of it is
// kept. A failure is recorded whatever leg produced it (red is a fact)
// and a skip grants no evidence, so neither says the eligible selection
// granted anything (REQ-check-witness-selection).
func (tr *TestRun) Granted() int {
	n := 0
	for _, o := range tr.Outcomes {
		if o == TestPassed {
			n++
		}
	}
	return n
}

// OutcomeRank orders outcomes worst-first for every merge that folds
// several results of one test into one: a red occurrence is never
// papered over by a green sibling, and every merge ranks alike.
func OutcomeRank(o TestOutcome) int {
	switch o {
	case TestFailed:
		return 3
	case TestPassed:
		return 2
	case TestSkipped:
		return 1
	}
	return 0
}
