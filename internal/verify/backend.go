package verify

import "sort"

// The backend contracts a language backend implements for verification,
// and the machinery that opens, closes, and selects among backends.

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

// WitnessClassifier is an optional Backend extension: it resolves, from
// the code, what class of witness a bound test yields.
type WitnessClassifier interface {
	WitnessClass(symbol string) WitnessClass
}

// WitnessClassVerdicts is an optional refinement of WitnessClassifier:
// the class plus, for an example classification, the verdict naming
// what the bound body lacks.
type WitnessClassVerdicts interface {
	WitnessClassVerdict(symbol string) (WitnessClass, string)
}

// WitnessSeeding is an optional Backend extension: which of the asked
// witness subjects must execute every run because closure equivalence
// cannot carry their outcome, each with the reason serving refuses it
// — random-seeded property witnesses, whose driver draws the inputs it
// quantifies over from a run-time seed no fingerprint pins
// (REQ-go-witness-class's seeded form), and subjects the backend cannot
// classify at all, since absence of proof never serves an outcome
// (REQ-evidence-witness-freshness); the two are named distinctly so a
// load gap never reads as a property witness. One call answers the
// whole set; an error is a classification fault the caller fails
// closed on.
type WitnessSeeding interface {
	NeverServe(symbols []string) (map[string]string, error)
}

// SymbolLocator is an optional Backend extension: the symbol's owning
// package as the backend resolves it. A symbol string alone cannot be
// split reliably (dotted path elements vs method receivers), so any
// package-scoped correlation reads this one source, never a re-parse.
type SymbolLocator interface {
	SymbolPackage(symbol string) (string, error)
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

// Quiescer is an optional Backend extension: the backend releases what
// answering has cost it — an owned child process, its loaded program —
// once a pass has asked every question it puts before executing, and
// answers later questions from what it kept (a question it never heard
// may cost the child again). The witness run calls it right after its
// seeding classification, the last question, so nothing of the
// resolution's cost is resident through the run's loads and executions
// (REQ-evidence-resolution-freshness-quiesce). The records the backend owes
// publish here, as at its close; the close publishes what was asked
// since.
type Quiescer interface {
	Quiesce()
}

// Backend verifies symbol references for one language. Implementations
// live outside this package: the core never depends on a backend.
type Backend interface {
	// Resolve checks a symbol reference and, when resolved, returns the
	// symbol's current shape hash. A returned error is a verification
	// error (e.g. the tree fails to load), never an absence.
	Resolve(symbol string) (Resolution, string, error)
}

// CloseBackends releases an operation's backends — a backend that owns a
// child or a store closes it — and returns the serving path's account:
// every backend's Notices, read after its close, because the close is
// what publishes the resolution records and a publish refused or
// degraded is otherwise a fault nobody sees. The one closer every face
// uses, so the account is read at the only sound moment on each
// (REQ-evidence-resolution-freshness-account). Nil when no backend keeps one.
func CloseBackends(backends map[string]Backend) []string {
	// Backend order: the account's line order is a fact of the set,
	// never of a map walk.
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	var notices []string
	for _, name := range names {
		b := backends[name]
		if c, ok := b.(interface{ Close() error }); ok {
			_ = c.Close()
		}
		if a, ok := b.(interface{ Notices() []string }); ok {
			notices = append(notices, a.Notices()...)
		}
	}
	return notices
}

// Closer closes an operation's backends exactly once — on the success
// path for their account, or deferred on an error path — so a result's
// account is read after the one close that publishes, and a second call
// answers the same account. The one closer every pass uses.
func Closer(backends map[string]Backend) func() []string {
	closed := false
	var notices []string
	return func() []string {
		if !closed {
			closed = true
			notices = CloseBackends(backends)
		}
		return notices
	}
}

// SeedingOf is the Go backend's seeding view, nil when the operation
// holds none.
func SeedingOf(backends map[string]Backend) WitnessSeeding {
	if seeding, ok := backends["go"].(WitnessSeeding); ok {
		return seeding
	}
	return nil
}

// FloorPackage is one package of the slice's sound floor with its
// disposition (REQ-go-slice): "declared" when the declaration frontier
// reaches it, "widened" when only the import closure does - reflection,
// init effects, blank imports, and build-tag selection can depend on it
// though no signature names it - and "external" for a first-hop
// dependency outside the loaded module, the floor's honest boundary.
type FloorPackage struct {
	Package     string
	Disposition string
}

// FloorSlicer is an optional Slicer extension: the package-level sound
// floor beside the declaration facts, so slice consumers get soundness
// by construction instead of a silently incomplete frontier.
type FloorSlicer interface {
	// SliceFloor computes the floor for the symbols; declaredPkgs names
	// the packages the caller's declaration frontier (Slice) already
	// reached, so one slicing pass serves both surfaces.
	SliceFloor(symbols []string, declaredPkgs []string) ([]FloorPackage, error)
}
