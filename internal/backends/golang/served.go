package golang

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/internal/trace"
	"github.com/greatliontech/stipulator/internal/verify"
)

var (
	typedLoadHookMu sync.Mutex
	typedLoadHook   func()
)

func noteTypedLoad() {
	typedLoadHookMu.Lock()
	defer typedLoadHookMu.Unlock()
	if typedLoadHook != nil {
		typedLoadHook()
	}
}

// Served is the verification backend that answers a binding's
// resolution from a multi-view SourceResult record proven fresh by gofresh and
// loads the typed backend only for the stale remainder
// (REQ-evidence-resolution-freshness). One per operation, constructed
// with the operation's whole symbol set — every bound symbol and every
// witness subject — so the served and stale sets are known before any
// typed load starts. It performs the backend's roles for verification: symbol
// resolution, package location, witness classification, and the
// serving refusals of random-seeded witnesses; the slice roles that
// read declarations use the typed backend: NewWholeTree builds the whole-tree
// form — no record served or published, no policy read, the typed load
// over the whole tree.
type Served struct {
	ctx context.Context
	dir string
	// sample is this backend's toolchain sampler — one judged
	// operation's memo, minted with the backend; its selection engines
	// sample through it.
	sample toolchainSample
	// whole marks declaration reading: nothing is served or published.
	// A nil set loads the whole tree; NewDeclarations supplies an explicit
	// set. Neither form reads selections until the first declaration query.
	whole  bool
	sels   []buildSelection
	served map[string]resolutioncache.Record
	// set is the operation's symbol set. When it names symbols the
	// typed load is scoped to their stale remainder and a symbol outside the
	// set is refused, never forwarded (admits); a backend built for the
	// unscoped declaration-reading form names none and loads the whole tree.
	set map[string]bool
	// inventory is metadata only: package boundaries are established
	// before capture and typed scope, never guessed from a dot position.
	inventory map[string]map[string]bool
	// unserved is every requested symbol without a served record — the
	// typed load's scope and the opening capture's subjects.
	unserved        []string
	initialEndpoint *selectionEndpoint
	// openingViews holds, per selection key, the SourceResult captures taken
	// when the typed load opened: a record publishes only when its closing
	// multi-view capture equals its opening one under unchanged package
	// boundaries (REQ-evidence-resolution-freshness-typed-load).
	openingViews  map[string]sourceViewCapture
	checkedViews  map[string]sourceViewCapture
	reopened      bool
	backend       *Backend
	backendErr    error
	backendLoaded bool
	typedLoads    int
	answers       map[string]symbolAnswer
	pending       map[string][]string
	// refusals holds, per symbol the typed backend was asked about, its serving
	// refusal or "" — the absence is an answer too.
	refusals map[string]string
	// reasons names, per recorded symbol that did not serve, why: the
	// freshness verdict's reason, or the vanishing — the operator's
	// account of a typed resolution.
	reasons   map[string]resolutionReason
	degraded  []string
	published map[string]publishAccount
}

var (
	_ verify.Backend           = (*Served)(nil)
	_ verify.Slicer            = (*Served)(nil)
	_ verify.FloorSlicer       = (*Served)(nil)
	_ verify.SymbolLocator     = (*Served)(nil)
	_ verify.WitnessClassifier = (*Served)(nil)
	_ verify.WitnessSeeding    = (*Served)(nil)
)

// symbolAnswer is one symbol's typed resolution this run, kept so the
// record it publishes and every later reader share one answer.
type symbolAnswer struct {
	// resolved marks the resolution half answered; classed the class
	// half — either may arrive first, and neither stands in for the
	// other.
	resolved  bool
	res       verify.Resolution
	shape     string
	selection string
	pkg       string
	pkgKnown  bool
	class     verify.WitnessClass
	reason    string
	classed   bool
	err       error
}

// NewServed prepares the serving form of the backend: records loaded,
// each selection's recorded subjects viewed under that selection's
// SourceResult session, multi-view precedence and fingerprints verified,
// closing interval re-observed, the valid ones served, everything else
// stale — a fault on the serving path degrades the affected symbols to
// the typed resolution, never the operation
// (REQ-evidence-freshness-degrade). An empty symbol set serves nothing
// and scopes the typed load to the whole tree; the policy is read either
// way, since every record it publishes is keyed by a selection.
func NewServed(ctx context.Context, dir string, symbols []string) (*Served, error) {
	servedStart := time.Now()
	s, err := newServed(ctx, dir)
	if err != nil {
		return nil, err
	}
	abs := s.dir
	sels, _, err := policyBuildSelections(abs)
	if err != nil {
		return nil, err
	}
	s.sels = sels
	endpoint := newSelectionEndpoint(ctx, abs, sels)
	s.initialEndpoint = endpoint
	if len(symbols) > 0 {
		wanted := make(map[string]bool, len(symbols))
		for _, sym := range symbols {
			wanted[sym] = true
		}
		s.set = wanted
		s.inventoryPackages(endpoint, symbols)
		s.serveRecords(endpoint)
	} else if beforeServingCloseForTest != nil {
		beforeServingCloseForTest()
	}
	for _, sym := range symbols {
		if _, ok := s.served[sym]; !ok {
			s.unserved = append(s.unserved, sym)
		}
	}
	trace.Emit(ctx, trace.DomainResolve, "new_served", s.dir,
		trace.Int("symbols", len(symbols)),
		trace.Int("served", len(s.served)),
		trace.Int("unserved", len(s.unserved)),
		trace.Int("degraded", len(s.degraded)),
		trace.Int64("duration_ms", time.Since(servedStart).Milliseconds()),
	)
	return s, nil
}

// Backends is the verification backend set an operation runs over: the
// served Go backend under its name, the one spelling every face and
// core reads (verify.CloseBackends releases it).
func Backends(ctx context.Context, dir string, symbols []string) (map[string]verify.Backend, error) {
	served, err := NewServed(ctx, dir, symbols)
	if err != nil {
		return nil, err
	}
	return BackendSet(served), nil
}

// BackendSet is the backend set over one Go backend of either form,
// under the backend's name — the one spelling of the set every face
// and core reads.
func BackendSet(s *Served) map[string]verify.Backend {
	return map[string]verify.Backend{"go": s}
}

// NewWholeTree prepares the whole-tree form: the typed path over the
// whole tree with nothing served or published and no selection read,
// so a policy the tree cannot parse refuses only where the typed path
// refuses it — at the first answer, through the typed load. The
// unscoped declaration-reading roles (blanket pinning, retargeting, impact,
// the declaration frontier and the partitions) take this form; explicit
// authoring uses NewDeclarations.
func NewWholeTree(ctx context.Context, dir string) (*Served, error) {
	s, err := newServed(ctx, dir)
	if err != nil {
		return nil, err
	}
	s.whole = true
	return s, nil
}

// newServed is the backend before its form is chosen.
func newServed(ctx context.Context, dir string) (*Served, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return &Served{
		sample:       newToolchainSample(),
		ctx:          ctx,
		dir:          abs,
		served:       map[string]resolutioncache.Record{},
		checkedViews: map[string]sourceViewCapture{},
		answers:      map[string]symbolAnswer{},
		pending:      map[string][]string{},
		refusals:     map[string]string{},
		reasons:      map[string]resolutionReason{},
	}, nil
}

func (s *Served) selection(key string) (buildSelection, bool) {
	for _, sel := range s.sels {
		if sel.key() == key {
			return sel, true
		}
	}
	return buildSelection{}, false
}

// subjectOf derives a subject from its declared package boundary.
func (s *Served) subjectOf(symbol string) (gofresh.Subject, bool) {
	pkg, ok := s.packageOf(symbol)
	if !ok {
		return gofresh.Subject{}, false
	}
	return gofresh.Subject{Package: pkg, Symbol: symbol[len(pkg)+1:]}, true
}

// loadPatterns is the typed load's scope: the packages of every
// unserved symbol. An unavailable inventory permits only the bounded
// syntactic candidates; an empty scope never widens to the whole tree.
func (s *Served) loadPatterns() []string {
	if len(s.unserved) == 0 && (!s.whole || s.set == nil) {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, sym := range s.unserved {
		candidates := symbolPackageCandidates(sym)
		if s.inventoryComplete() {
			pkg, ok := s.packageOf(sym)
			if !ok {
				continue
			}
			candidates = []string{pkg}
		}
		for _, pkg := range candidates {
			if !seen[pkg] {
				seen[pkg] = true
				out = append(out, pkg)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (s *Served) ensureBackend() (*Backend, error) {
	if !s.backendLoaded {
		if err := s.prepareDeclarations(); err != nil {
			return nil, err
		}
		// The opening capture precedes the typed load's snapshot: a record
		// publishes under a fingerprint no later than the answer it
		// carries.
		if !s.whole {
			openStart := time.Now()
			s.openingCapture()
			trace.Emit(s.ctx, trace.DomainResolve, "opening_capture", s.dir,
				trace.Int("unserved", len(s.unserved)),
				trace.Int64("duration_ms", time.Since(openStart).Milliseconds()),
			)
		}
		patterns := s.loadPatterns()
		s.typedLoads++
		noteTypedLoad()
		loadStart := time.Now()
		b, err := newContext(s.ctx, s.dir, patterns)
		trace.Emit(s.ctx, trace.DomainResolve, "typed_load", s.dir,
			trace.Int("patterns", len(patterns)),
			trace.Int64("duration_ms", time.Since(loadStart).Milliseconds()),
			trace.Err(err),
		)
		if err != nil {
			if s.ctx.Err() == nil {
				s.backendLoaded = true
				s.backendErr = err
			}
			return nil, err
		}
		s.backend = b
		s.backendLoaded = true
	}
	return s.backend, s.backendErr
}

// Slice implements verify.Slicer through the typed backend: the
// declaration-reading roles have no served form.
func (s *Served) Slice(symbols []string) ([]verify.Decl, error) {
	b, err := s.ensureBackend()
	if err != nil {
		return nil, err
	}
	return b.Slice(symbols)
}

// SliceFloor implements verify.FloorSlicer through the typed backend.
func (s *Served) SliceFloor(symbols []string, declaredPkgs []string) ([]verify.FloorPackage, error) {
	b, err := s.ensureBackend()
	if err != nil {
		return nil, err
	}
	return b.SliceFloor(symbols, declaredPkgs)
}

// typed resolves one symbol through the typed backend, once per run.
func (s *Served) typed(symbol string) symbolAnswer {
	if a, ok := s.answers[symbol]; ok && a.resolved {
		return a
	}
	a := s.answers[symbol]
	if !s.admits(symbol) {
		a.resolved = true
		a.err = outsideSet(symbol)
		s.answers[symbol] = a
		return a
	}
	if err := s.ctx.Err(); err != nil {
		a.err = err
		s.answers[symbol] = a
		return a
	}
	b, err := s.ensureBackend()
	if err != nil {
		if s.ctx.Err() == nil {
			a.resolved = true
		}
		a.err = err
		s.answers[symbol] = a
		return a
	}
	res, shape, selection, err := b.ResolveIn(symbol)
	if err == nil || s.ctx.Err() == nil {
		a.resolved = true
	}
	a.res, a.shape, a.selection, a.err = res, shape, selection, err
	// Every resolved symbol's package is read once so later callers
	// (including after Quiesce) and published records share the answer;
	// the serving form also queues found symbols for publication.
	if err == nil {
		a.pkg = b.SymbolPackage(symbol)
		a.pkgKnown = true
		if !s.whole && res != verify.NotFound && a.pkg != "" {
			s.pending[selection] = append(s.pending[selection], symbol)
		}
	}
	s.answers[symbol] = a
	return a
}

// Resolve implements verify.Backend.
func (s *Served) Resolve(symbol string) (verify.Resolution, string, error) {
	res, shape, _, err := s.ResolveIn(symbol)
	return res, shape, err
}

// ResolveIn is Resolve naming the resolving build selection.
func (s *Served) ResolveIn(symbol string) (verify.Resolution, string, string, error) {
	if rec, ok := s.served[symbol]; ok {
		res, _ := resolutionFromWire(rec.Resolution)
		return res, rec.Shape, rec.Selection, nil
	}
	a := s.typed(symbol)
	return a.res, a.shape, a.selection, a.err
}

// SymbolFile answers a symbol's declaring file through the typed backend:
// a record names a package, never a file.
func (s *Served) SymbolFile(symbol string) (string, bool, error) {
	b, err := s.ensureBackend()
	if err != nil {
		return "", false, err
	}
	file, found := b.SymbolFile(symbol)
	return file, found, nil
}

// ReachedPackages answers the packages a set of changed files reaches
// through the typed backend.
func (s *Served) ReachedPackages(files []string) (map[string]bool, error) {
	b, err := s.ensureBackend()
	if err != nil {
		return nil, err
	}
	return b.ReachedPackages(files), nil
}

// SymbolPackage implements verify.SymbolLocator.
func (s *Served) SymbolPackage(symbol string) (string, error) {
	if rec, ok := s.served[symbol]; ok {
		return rec.Package, nil
	}
	if a, ok := s.answers[symbol]; ok && a.err == nil && a.pkgKnown {
		return a.pkg, nil
	}
	if !s.admits(symbol) {
		return "", outsideSet(symbol)
	}
	b, err := s.ensureBackend()
	if err != nil {
		return "", err
	}
	pkg := b.SymbolPackage(symbol)
	a := s.answers[symbol]
	a.pkg = pkg
	a.pkgKnown = true
	s.answers[symbol] = a
	return pkg, nil
}

// admits is the symbol set's own — the one admission every
// symbol-resolving role consults (resolution, classification, package
// location, serving refusal), so the boundary cannot drift between
// them. A nil set is a backend that named no symbols — the whole-tree
// form, or a serving operation whose set came up empty — and admits
// every symbol; its typed load covers the whole tree. The declaration-reading
// roles (Slice, SliceFloor, SymbolFile) and ReachedPackages, which
// takes files, read the tree as it is and consult no set.
func (s *Served) admits(symbol string) bool {
	if len(symbolPackageCandidates(symbol)) == 0 {
		return true
	}
	return s.set == nil || s.set[symbol]
}

// outsideSet is the refusal for a symbol the operation never named:
// the typed load is scoped to the set's stale remainder, so forwarding an
// outside symbol would answer not found for what the tree declares.
func outsideSet(symbol string) error {
	return fmt.Errorf("symbol %q is outside this operation's symbol set", symbol)
}

// classify answers a symbol's witness class and reason through the
// typed backend, once per run.
func (s *Served) classify(symbol string) (verify.WitnessClass, string) {
	a := s.answers[symbol]
	if a.classed {
		return a.class, a.reason
	}
	if !s.admits(symbol) {
		return verify.ExampleWitness, outsideSet(symbol).Error()
	}
	b, err := s.ensureBackend()
	if err != nil {
		return verify.ExampleWitness, ""
	}
	class, reason := b.WitnessClassVerdict(symbol)
	a.class, a.reason, a.classed = class, reason, true
	s.answers[symbol] = a
	return class, reason
}

// WitnessClassVerdict implements verify.WitnessClassVerdicts.
func (s *Served) WitnessClassVerdict(symbol string) (verify.WitnessClass, string) {
	if rec, ok := s.served[symbol]; ok {
		class, _ := classFromWire(rec.WitnessClass)
		return class, rec.WitnessClassReason
	}
	return s.classify(symbol)
}

// WitnessClass implements verify.WitnessClassifier.
func (s *Served) WitnessClass(symbol string) verify.WitnessClass {
	class, _ := s.WitnessClassVerdict(symbol)
	return class
}

// ExplainWitness derives a witness's seeding chain through the typed backend:
// the derivation is the walk serving decided over, so a served record
// explains through the same typed load its refusal came from.
func (s *Served) ExplainWitness(symbol string) (gofresh.Chain, string, error) {
	if !s.admits(symbol) {
		return gofresh.Chain{}, "", outsideSet(symbol)
	}
	b, err := s.ensureBackend()
	if err != nil {
		return gofresh.Chain{}, "", err
	}
	return b.explainWitness(symbol)
}

// NeverServe implements verify.WitnessSeeding: served records answer
// their recorded refusal; the rest ask the typed backend in one batch.
func (s *Served) NeverServe(symbols []string) (map[string]string, error) {
	out := map[string]string{}
	var ask []string
	for _, symbol := range symbols {
		if rec, ok := s.served[symbol]; ok {
			if rec.NeverServe != "" {
				out[symbol] = rec.NeverServe
			}
			continue
		}
		if !s.admits(symbol) {
			return nil, outsideSet(symbol)
		}
		ask = append(ask, symbol)
	}
	var unasked []string
	for _, symbol := range ask {
		if why, asked := s.refusals[symbol]; asked {
			if why != "" {
				out[symbol] = why
			}
			continue
		}
		unasked = append(unasked, symbol)
	}
	ask = unasked
	if len(ask) == 0 {
		return out, nil
	}
	b, err := s.ensureBackend()
	if err != nil {
		return nil, err
	}
	if !s.whole {
		for _, symbol := range ask {
			s.typed(symbol)
		}
	}
	refusals, err := b.NeverServe(ask)
	if err != nil {
		return nil, err
	}
	for symbol, why := range refusals {
		out[symbol] = why
	}
	s.refusals = mergeRefusals(s.refusals, ask, refusals)
	return out, nil
}

// Close publishes a record for every symbol the typed backend resolved
// since the last quiesce — the typed answer, its class, its serving
// refusal — and releases the typed backend.
func (s *Served) Close() error {
	s.Quiesce()
	return nil
}

// Quiesce implements verify.Quiescer: the records the typed backend's
// answers owe publish now and the typed backend is released, while
// every answer this run gathered — resolutions, packages, classes,
// serving refusals — stays to be served from memory
// (REQ-evidence-resolution-freshness-quiesce).
func (s *Served) Quiesce() {
	defer func() {
		hadBackend := s.backend != nil
		s.backend = nil
		s.backendErr = nil
		s.backendLoaded = false
		s.initialEndpoint = nil
		s.checkedViews = nil
		s.openingViews = nil
		s.reopened = true
		if hadBackend {
			debug.FreeOSMemory()
		}
	}()
	if s.backend == nil {
		return
	}
	if s.ctx.Err() == nil {
		var unasked []string
		for symbol, a := range s.answers {
			if a.err != nil {
				continue
			}
			if !a.pkgKnown {
				a.pkg = s.backend.SymbolPackage(symbol)
				a.pkgKnown = true
				s.answers[symbol] = a
			}
			if !a.classed {
				s.classify(symbol)
			}
			if _, asked := s.refusals[symbol]; !asked {
				unasked = append(unasked, symbol)
			}
		}
		if len(unasked) > 0 {
			if refusals, err := s.backend.NeverServe(unasked); err == nil {
				s.refusals = mergeRefusals(s.refusals, unasked, refusals)
			}
		}
	}
	if len(s.pending) == 0 {
		return
	}
	s.publishPending()
	s.pending = map[string][]string{}
}

// mergeRefusals records, for every asked symbol, the typed backend's refusal or
// its absence — the absence is an answer too.
func mergeRefusals(into map[string]string, asked []string, refusals map[string]string) map[string]string {
	if into == nil {
		into = map[string]string{}
	}
	for _, symbol := range asked {
		into[symbol] = refusals[symbol]
	}
	return into
}

// witnessSymbols names every witness subject of the captured policy —
// "package.Test" for each capture group's subjects.
func witnessSymbols(ctx context.Context, pc *Capture) ([]string, error) {
	d, err := pc.discover(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, g := range d.groups {
		for _, s := range groupSubjects(g) {
			key := s.Package + "." + s.Symbol
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// OperationSymbols is the served set of one verifying operation: the
// bound symbols and the captured policy's witness subjects.
func OperationSymbols(ctx context.Context, store *records.Store, pc *Capture) ([]string, error) {
	var witnesses []string
	if pc != nil {
		var err error
		if witnesses, err = witnessSymbols(ctx, pc); err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, sym := range append(store.BoundSymbols("go"), witnesses...) {
		if !seen[sym] {
			seen[sym] = true
			out = append(out, sym)
		}
	}
	sort.Strings(out)
	return out, nil
}

// GCResolutions removes the corpus's resolution records no operation
// can serve: a record whose symbol no binding names and no witness
// subject of the captured policy carries (REQ-evidence-store-gc).
func GCResolutions(ctx context.Context, dir string, store *records.Store, pc *Capture) (removed, kept int, err error) {
	symbols, err := OperationSymbols(ctx, store, pc)
	if err != nil {
		return 0, 0, err
	}
	live := make(map[string]bool, len(symbols))
	for _, sym := range symbols {
		live[sym] = true
	}
	return resolutioncache.GC(ctx, dir, func(_, symbol string) bool { return live[symbol] })
}
