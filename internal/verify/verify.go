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
	"fmt"
	"sort"
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
	// Unverified: no backend was available for the binding.
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
	// RaceEnabled records whether the run enabled the race detector: a
	// rigor attribute every witness inherits.
	RaceEnabled bool
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
	// PackageFailures carries the failure diagnostics no single test owns
	// — an envelope cutoff, a package abort, a build failure — keyed by
	// import path, occurrences joined like Failures: an expected subject
	// denied an outcome must be diagnosable from the run that denied it.
	PackageFailures map[string]string
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
	Backend       string
	Role          stipulatorv1.BindingRole
	// ContentPinned reports whether the content-hash pin matches the
	// requirement's current hash.
	ContentPinned bool
	Resolution    Resolution
	Shape         ShapeState
	// TestOutcome is set for tests- and proves-role bindings when the run
	// witnessed tests; WitnessClass and RaceEnabled qualify the witness.
	TestOutcome  TestOutcome
	WitnessClass WitnessClass
	RaceEnabled  bool
}

// WitnessClass classifies what a bound test quantifies over.
type WitnessClass int

const (
	// ExampleWitness: the test exercises named cases.
	ExampleWitness WitnessClass = iota
	// PropertyWitness: the test is generator-driven, quantifying over
	// inputs (e.g. a fuzz target).
	PropertyWitness
	// AnalyzerProof: the test's assertions are recognized analyzer calls
	// (the stipulate/structural library) — the proof tier.
	AnalyzerProof
)

// witnessRole reports whether a binding's role carries a test outcome:
// tests and proves both name executable symbols.
func witnessRole(role stipulatorv1.BindingRole) bool {
	return role == stipulatorv1.BindingRole_BINDING_ROLE_TESTS ||
		role == stipulatorv1.BindingRole_BINDING_ROLE_PROVES
}

// WitnessClassifier is an optional Backend extension: it resolves, from
// the code, what class of witness a bound test yields.
type WitnessClassifier interface {
	WitnessClass(symbol string) WitnessClass
}

// Decl is one declaration fact from a code slice.
type Decl struct {
	Package     string
	Name        string
	Declaration string
	ShapeHash   string
}

// Slicer is an optional Backend extension: the declarations of the
// transitive dependency frontier of symbols — facts only.
type Slicer interface {
	Slice(symbols []string) ([]Decl, error)
}

// Backend verifies symbol references for one language. Implementations
// live outside this package: the core never depends on a backend.
type Backend interface {
	// Resolve checks a symbol reference and, when resolved, returns the
	// symbol's current shape hash. A returned error is a verification
	// error (e.g. the tree fails to load), never an absence.
	Resolve(symbol string) (Resolution, string, error)
}

// Report is the outcome of a verification run.
type Report struct {
	Problems []Problem
	// Results holds the verified state of every well-formed binding, in
	// store order.
	Results []BindingResult
	// Pinned counts bindings whose content-hash pin matches the current
	// corpus; Stale counts bindings whose pin is unset or differs.
	Pinned, Stale int
	// ShapePinned, ShapeUnpinned, and ShapeMismatch count resolved
	// bindings by shape-pin state; Broken counts bindings whose symbol
	// did not resolve; Unverified counts bindings whose backend has no
	// verifier in this run.
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
	// PackageFailures carries the witnessed run's failure diagnostics no
	// single test owns — an envelope cutoff, a package abort, a build
	// failure — keyed by import path: a bound test reading unwitnessed is
	// diagnosable from the same report that says so. Nil in unwitnessed
	// runs.
	PackageFailures map[string]string
	// Attestations holds the verified state of every well-formed
	// requirement attestation, in store order.
	Attestations []AttestationResult
	// Signatures classify each changed requirement's shape against the
	// record pins — the baseline; no verification outcome is persisted
	// (REQ-gate-change-signature). Derived only in witnessed runs, in
	// requirement order.
	Signatures []ChangeSignature
}

// ChangeSignature labels one requirement's change shape.
type ChangeSignature struct {
	RequirementId string
	Label         SignatureLabel
	// Evidence names the observations behind the label, human-readable.
	Evidence []string
}

// SignatureLabel is the change-signature vocabulary.
type SignatureLabel int

const (
	// Rearchitecture: structure moved — a proof-shape pin no longer
	// matches, or a proof failed — while every behavior witness stayed
	// green: the behavior contract is intact under a new shape.
	Rearchitecture SignatureLabel = iota + 1
	// SemanticDrift: a behavior witness failed while the requirement's
	// content pin is current — red with no corresponding spec delta:
	// behavior diverged under a stable contract.
	SemanticDrift
)

// AttestationResult is one requirement attestation checked against the
// current corpus: the reason rides to coverage, and a stale content pin
// means the requirement moved since it was vouched for.
type AttestationResult struct {
	RequirementId string
	Reason        string
	ContentPinned bool
}

// Run checks the store against the compiled spec, resolving symbols
// through the supplied backends (keyed by backend name; nil skips all
// symbol resolution) and correlating test outcomes from testRun (nil
// skips witnessing: role-tests bindings read TestNotRun).
func Run(spec *stipulatorv1.Spec, store *records.Store, backends map[string]Backend, testRun *TestRun) *Report {
	hashes := map[string]string{}
	for _, r := range spec.GetRequirements() {
		hashes[r.GetId()] = r.GetContentHash()
	}

	rep := &Report{}
	problem := func(path, format string, args ...any) {
		rep.Problems = append(rep.Problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}

	seen := map[string]bool{}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			id := b.GetRequirementId()
			key := id + "|" + b.GetBackend() + "|" + b.GetSymbol() + "|" + b.GetRole().String()
			if seen[key] {
				problem(bf.Path, "duplicate binding: %s %s %s", id, b.GetSymbol(), b.GetRole())
			}
			seen[key] = true

			malformed := false
			if id == "" {
				problem(bf.Path, "binding without requirement_id")
				malformed = true
			}
			if b.GetBackend() == "" {
				problem(bf.Path, "binding for %s has no backend", id)
				malformed = true
			}
			if b.GetSymbol() == "" {
				problem(bf.Path, "binding for %s has no symbol", id)
				malformed = true
			}
			if b.GetRole() == stipulatorv1.BindingRole_BINDING_ROLE_UNSPECIFIED {
				problem(bf.Path, "binding for %s has no role", id)
				malformed = true
			}
			h, known := hashes[id]
			if id != "" && !known {
				problem(bf.Path, "binding names %s, which is not in the corpus", id)
				malformed = true
			}
			if malformed {
				continue
			}

			result := BindingResult{
				Path:          bf.Path,
				RequirementId: id,
				Symbol:        b.GetSymbol(),
				Backend:       b.GetBackend(),
				Role:          b.GetRole(),
				ContentPinned: b.GetContentHash() == h,
				Resolution:    Unverified,
				Shape:         ShapeUnknown,
			}
			if result.ContentPinned {
				rep.Pinned++
			} else {
				rep.Stale++
			}

			if testRun != nil && witnessRole(b.GetRole()) {
				result.TestOutcome = testRun.Outcomes[b.GetSymbol()]
				// RaceEnabled qualifies a witness; a row without a passing
				// outcome carries no witness to qualify, so it never claims
				// the run's rigor for an outcome another invocation (or no
				// execution at all) produced.
				result.RaceEnabled = testRun.RaceEnabled && result.TestOutcome == TestPassed
				if wc, ok := backends[b.GetBackend()].(WitnessClassifier); ok {
					result.WitnessClass = wc.WitnessClass(b.GetSymbol())
				}
				switch result.TestOutcome {
				case TestPassed:
					rep.TestsPassed++
				case TestFailed:
					rep.TestsFailed++
				case TestNotRun:
					// No outcome in a witnessed run: the test never ran
					// (package build failure, sibling panic aborting the
					// binary) — unwitnessed, reads as broken.
					rep.TestsNotRun++
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
					rep.Broken++
				default:
					result.Resolution = Resolved
					switch {
					case b.GetShapeHash() == "":
						result.Shape = ShapeUnpinned
						rep.ShapeUnpinned++
					case b.GetShapeHash() == shape:
						result.Shape = ShapeMatch
						rep.ShapePinned++
					default:
						result.Shape = ShapeMismatch
						rep.ShapeMismatch++
					}
				}
			} else {
				rep.Unverified++
			}
			rep.Results = append(rep.Results, result)
		}
	}

	if testRun != nil {
		rep.OutsidePolicy = testRun.OutsidePolicy
		rep.PackageFailures = testRun.PackageFailures
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

	gappedIDs := map[string]bool{}
	for _, gf := range store.Gaps {
		gappedIDs[gf.Gap.GetRequirementId()] = true
	}
	attestedIDs := map[string]string{}
	for _, af := range store.Attestations {
		for _, a := range af.Set.GetAttestations() {
			id := a.GetRequirementId()
			switch {
			case id == "":
				problem(af.Path, "attestation without requirement_id")
				continue
			case a.GetReason() == "":
				problem(af.Path, "attestation for %s has no reason", id)
				continue
			}
			if prior, dup := attestedIDs[id]; dup {
				problem(af.Path, "attestation for %s duplicates %s; one judgment per requirement", id, prior)
				continue
			}
			attestedIDs[id] = af.Path
			hash, known := hashes[id]
			if !known {
				problem(af.Path, "attestation names %s, which is not in the corpus", id)
				continue
			}
			if gappedIDs[id] {
				// Deferred and judged-satisfied contradict: the records
				// cannot both stand.
				problem(af.Path, "%s is both gapped and attested; the records contradict — retract one", id)
				continue
			}
			rep.Attestations = append(rep.Attestations, AttestationResult{
				RequirementId: id,
				Reason:        a.GetReason(),
				ContentPinned: a.GetContentHash() == hash,
			})
		}
	}

	seenGaps := map[string]string{}
	for _, gf := range store.Gaps {
		id := gf.Gap.GetRequirementId()
		if id == "" {
			problem(gf.Path, "gap without requirement_id")
		} else if _, known := hashes[id]; !known {
			problem(gf.Path, "gap names %s, which is not in the corpus", id)
		}
		if id != "" {
			if prior, dup := seenGaps[id]; dup {
				problem(gf.Path, "gap for %s duplicates %s; one declaration per requirement", id, prior)
			} else {
				seenGaps[id] = gf.Path
			}
		}
		if gf.Gap.GetReason() == "" {
			problem(gf.Path, "gap for %s has no reason", id)
		}
		// Landing-condition targets are deliberately not resolved here:
		// exists(...) and covered(...) may name requirements the spec does
		// not hold yet — that prospectiveness is their purpose.
		if !gf.Gap.HasLands() {
			problem(gf.Path, "gap for %s has no landing condition", id)
		}
	}

	sort.Slice(rep.Problems, func(i, j int) bool {
		a, b := rep.Problems[i], rep.Problems[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Message < b.Message
	})
	if testRun != nil {
		rep.Signatures = signatures(rep.Results)
	}
	return rep
}

// signatures classifies each requirement's change shape from one run's
// binding results, with the record pins as baseline
// (REQ-gate-change-signature). The two labels are disjoint by
// construction: rearchitecture demands every behavior witness green,
// semantic drift demands one red.
func signatures(results []BindingResult) []ChangeSignature {
	type reqState struct {
		proofMoved, proofFailed  []string
		behaviorGreen            int
		redCurrent, redStalePins []string
	}
	states := map[string]*reqState{}
	order := []string{}
	get := func(id string) *reqState {
		st, ok := states[id]
		if !ok {
			st = &reqState{}
			states[id] = st
			order = append(order, id)
		}
		return st
	}
	for _, r := range results {
		st := get(r.RequirementId)
		proof := r.Role == stipulatorv1.BindingRole_BINDING_ROLE_PROVES || r.WitnessClass == AnalyzerProof
		switch {
		case proof && r.Shape == ShapeMismatch:
			st.proofMoved = append(st.proofMoved, r.Symbol)
		case proof && r.TestOutcome == TestFailed:
			st.proofFailed = append(st.proofFailed, r.Symbol)
		case !proof && witnessRole(r.Role):
			switch r.TestOutcome {
			case TestPassed:
				st.behaviorGreen++
			case TestFailed:
				if r.ContentPinned {
					st.redCurrent = append(st.redCurrent, r.Symbol)
				} else {
					st.redStalePins = append(st.redStalePins, r.Symbol)
				}
			}
		}
	}
	var out []ChangeSignature
	sort.Strings(order)
	for _, id := range order {
		st := states[id]
		switch {
		case len(st.redCurrent) > 0:
			var ev []string
			for _, sym := range st.redCurrent {
				ev = append(ev, "behavior witness failed under a current contract: "+sym)
			}
			for _, sym := range st.redStalePins {
				ev = append(ev, "behavior witness failed alongside a spec delta: "+sym)
			}
			out = append(out, ChangeSignature{RequirementId: id, Label: SemanticDrift, Evidence: ev})
		case (len(st.proofMoved) > 0 || len(st.proofFailed) > 0) && len(st.redCurrent)+len(st.redStalePins) == 0 && st.behaviorGreen > 0:
			var ev []string
			for _, sym := range st.proofMoved {
				ev = append(ev, "proof shape moved: "+sym)
			}
			for _, sym := range st.proofFailed {
				ev = append(ev, "proof failed: "+sym)
			}
			ev = append(ev, fmt.Sprintf("behavior green: %d witnesses", st.behaviorGreen))
			out = append(out, ChangeSignature{RequirementId: id, Label: Rearchitecture, Evidence: ev})
		}
	}
	return out
}
