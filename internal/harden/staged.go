package harden

import (
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// SurfaceClass is one disposition of a changed source surface against the
// body mutator (REQ-harden-staged-scope). Exactly one applies to each entry.
type SurfaceClass string

const (
	// Covered: a bound implements symbol with a resolving witness whose
	// class the body mutator can break — harden covers it.
	Covered SurfaceClass = "covered"
	// UnboundImpl: a changed function or method with no implements binding.
	UnboundImpl SurfaceClass = "unbound-impl"
	// NoWitness: a bound implements symbol with no resolving witness.
	NoWitness SurfaceClass = "no-witness"
	// WitnessClassOutside: a bound implements symbol whose only witnesses
	// are analyzer proofs, which body mutation does not exercise.
	WitnessClassOutside SurfaceClass = "witness-class-outside-operators"
	// GeneratedOrData: a non-Go or generated file — not a mutation surface.
	GeneratedOrData SurfaceClass = "generated-or-data"
	// IntegrationSeam: a changed Go file declaring no top-level function or
	// method — changed behavior that is not itself a bound body.
	IntegrationSeam SurfaceClass = "integration-seam"
)

// SurfaceEntry is one classified surface: a file-level bucket (Symbol empty)
// or a per-symbol disposition.
type SurfaceEntry struct {
	Path         string
	Symbol       string
	Class        SurfaceClass
	Requirements []string
}

// StagedReport is the staged-delta classification: every changed surface
// dispositioned, plus the coverable subset for a one-line summary. It is
// exploration, never a gate (REQ-harden-exploration).
type StagedReport struct {
	Entries []SurfaceEntry
}

// SurfaceBackend is the backend capability the staged classifier needs: the
// Go content of the changed files, and the class of a witness symbol (so a
// symbol witnessed only by analyzer proofs reads as outside body mutation).
// *golang.Backend satisfies it.
type SurfaceBackend interface {
	Surface(paths []string, head func(path string) ([]byte, bool)) []golang.FileSurface
	WitnessClass(symbol string) verify.WitnessClass
}

// Covered reports whether the mutator can reach any changed surface — the
// subset the operator runs harden over before hand-mutating the rest.
func (r *StagedReport) Coverable() []SurfaceEntry {
	var out []SurfaceEntry
	for _, e := range r.Entries {
		if e.Class == Covered {
			out = append(out, e)
		}
	}
	return out
}

// StagedScope classifies each changed source surface against the body
// mutator: covered symbols harden can run, and the specific reason it cannot
// reach the rest. changed is the tree-relative changed-file set (gitfs.Changed
// against HEAD); head supplies a path's HEAD bytes so only symbols whose body
// actually changed are reported. The result is advisory — its job is to make
// the manual-mutation tail explicit (REQ-harden-staged-scope), never to gate.
func StagedScope(spec *stipulatorv1.Spec, store *records.Store, backend SurfaceBackend, changed []string, head func(path string) ([]byte, bool)) *StagedReport {
	// Bound implements symbols and their witness union, keyed by symbol.
	targets := map[string]Target{}
	for _, t := range Plan(spec, store, nil, nil) {
		targets[t.Symbol] = t
	}

	rep := &StagedReport{}
	for _, s := range backend.Surface(changed, head) {
		if !s.IsGo || s.Generated {
			rep.Entries = append(rep.Entries, SurfaceEntry{Path: s.Path, Class: GeneratedOrData})
			continue
		}
		if len(s.Symbols) == 0 {
			rep.Entries = append(rep.Entries, SurfaceEntry{Path: s.Path, Class: IntegrationSeam})
			continue
		}
		for _, sym := range s.Symbols {
			e := SurfaceEntry{Path: s.Path, Symbol: sym}
			t, bound := targets[sym]
			switch {
			case !bound:
				e.Class = UnboundImpl
			case len(t.Witnesses) == 0:
				e.Class = NoWitness
				e.Requirements = t.Requirements
			case mutatable(backend, t.Witnesses):
				e.Class = Covered
				e.Requirements = t.Requirements
			default:
				e.Class = WitnessClassOutside
				e.Requirements = t.Requirements
			}
			rep.Entries = append(rep.Entries, e)
		}
	}
	sort.SliceStable(rep.Entries, func(i, j int) bool {
		a, b := rep.Entries[i], rep.Entries[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Symbol < b.Symbol
	})
	return rep
}

// mutatable reports whether any witness in the set is a class the body
// mutator exercises — an example or property test. Analyzer proofs assert
// structure the body mutator does not perturb, so a symbol witnessed only by
// proofs is outside its reach.
func mutatable(backend SurfaceBackend, witnesses []string) bool {
	for _, w := range witnesses {
		if backend.WitnessClass(w) != verify.AnalyzerProof {
			return true
		}
	}
	return false
}
