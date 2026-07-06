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

// VacuityChecker is an optional Backend extension: whether a test symbol
// contains no failure path — no failing testing call, no delegation to a
// callee receiving a testing handle, no panic. Resolved from the code,
// never declared.
type VacuityChecker interface {
	Vacuous(symbol string) (bool, error)
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
				result.RaceEnabled = testRun.RaceEnabled
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

	for _, gf := range store.Gaps {
		id := gf.Gap.GetRequirementId()
		if id == "" {
			problem(gf.Path, "gap without requirement_id")
		} else if _, known := hashes[id]; !known {
			problem(gf.Path, "gap names %s, which is not in the corpus", id)
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
	return rep
}
