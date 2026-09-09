package golang

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// selectionEngine is the freshness engine for one resolution build
// selection: it loads the selection's sources exactly as the resolver
// child's typed view does — the same environment and tag flags
// (selectionViewEnv, selectionViewFlags) — so a resolution record's
// fingerprint is captured and checked under the view that produced the
// resolution (REQ-evidence-resolution-freshness). Resolution observes
// no runtime input, so every check closes its own window: no deferred
// close, no producer environment.
func selectionEngine(ctx context.Context, dir string, sel buildSelection) (*gofresh.Engine, error) {
	base, err := goworkEnv(dir)
	if err != nil {
		return nil, err
	}
	env := selectionViewEnv(base, sel)
	// The same provenance prerequisite the child's typed view enforces,
	// in the same form: an identified selection toolchain this binary's
	// frontend cannot read refuses before any verdict, while a toolchain
	// that cannot be sampled loads no view (REQ-evidence-toolchain-
	// provenance; newContext) — so a served view exists exactly when the
	// child's would.
	if err := checkToolchainSkewIdentified(dir, env); err != nil {
		return nil, err
	}
	return newEngine(ctx, dir, env, selectionViewFlags(sel))
}

// Served is the verification backend that answers a binding's
// resolution from a resolution record proven fresh by gofresh and opens
// the owned resolver child only for the stale remainder
// (REQ-evidence-resolution-freshness). One per operation, constructed
// with the operation's whole symbol set — every bound symbol and every
// witness subject — so the served and stale sets are known before any
// child starts. It performs the child's roles for verification: symbol
// resolution, package location, witness classification, and the
// serving refusals of random-seeded witnesses; the slice roles that
// read declarations keep the child (NewOwned).
type Served struct {
	ctx    context.Context
	dir    string
	sels   map[string]buildSelection
	served map[string]resolutioncache.Record
	// set is the operation's symbol set. When it names symbols the
	// child is scoped to their stale remainder and a symbol outside the
	// set is refused, never forwarded (admits); a backend built for the
	// declaration-reading roles names none, and its child is the whole
	// tree, which answers every symbol as the tree declares it.
	set map[string]bool
	// unserved is every requested symbol without a served record — the
	// child's scope and the opening capture's subjects.
	unserved []string
	// opening holds, per selection key, the fingerprints captured when
	// the child opened: a record publishes only when its closing
	// capture equals its opening one, so a resolution answered from the
	// child's snapshot is never recorded under a fingerprint of a tree
	// that moved after it (the straddle check).
	opening map[string]map[gofresh.Subject]gofresh.Fingerprint
	child   *Owned
	answers map[string]childAnswer
	pending map[string][]string
	// refusals holds, per symbol the child was asked about, its serving
	// refusal or "" — the absence is an answer too.
	refusals map[string]string
	// reasons names, per recorded symbol that did not serve, why: the
	// freshness verdict's reason, or the vanishing — the operator's
	// account of a typed resolution.
	reasons  map[string]string
	degraded []string
}

// childAnswer is one symbol's typed resolution this run, kept so the
// record it publishes and every later reader share one answer.
type childAnswer struct {
	// resolved marks the resolution half answered; classed the class
	// half — either may arrive first, and neither stands in for the
	// other.
	resolved  bool
	res       verify.Resolution
	shape     string
	selection string
	pkg       string
	class     verify.WitnessClass
	reason    string
	classed   bool
	err       error
}

// NewServed prepares the served backend: records loaded, each
// selection's recorded subjects viewed under that selection's engine
// and batch-checked, the valid ones served, everything else stale. A
// fault on the serving path degrades the affected symbols to the typed
// resolution, never the operation (REQ-evidence-freshness-degrade).
func NewServed(ctx context.Context, dir string, symbols []string) (*Served, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	sels, _, err := policyBuildSelections(abs)
	if err != nil {
		return nil, err
	}
	s := &Served{ctx: ctx, dir: abs, sels: map[string]buildSelection{}, served: map[string]resolutioncache.Record{}, answers: map[string]childAnswer{}, pending: map[string][]string{}, reasons: map[string]string{}}
	for _, sel := range sels {
		s.sels[SelectionKey(sel.tags, sel.toolchain)] = sel
	}
	wanted := map[string]bool{}
	for _, sym := range symbols {
		wanted[sym] = true
	}
	s.set = wanted
	bySelection := map[string][]resolutioncache.Record{}
	for _, rec := range resolutioncache.Load(abs) {
		if !wanted[rec.Symbol] {
			continue
		}
		if _, known := s.sels[rec.Selection]; !known {
			continue
		}
		bySelection[rec.Selection] = append(bySelection[rec.Selection], rec)
	}
	keys := make([]string, 0, len(bySelection))
	for key := range bySelection {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		s.serveSelection(key, bySelection[key])
	}
	for _, sym := range symbols {
		if _, ok := s.served[sym]; !ok {
			s.unserved = append(s.unserved, sym)
		}
	}
	return s, nil
}

// packageOf derives a symbol's import path — everything before the
// first dot after the last slash — the pattern its typed load needs;
// false for a symbol string that carries no package.
func packageOf(symbol string) (string, bool) {
	i := strings.LastIndex(symbol, "/")
	j := strings.Index(symbol[i+1:], ".")
	if j < 0 {
		return "", false
	}
	return symbol[:i+1+j], true
}

// childPatterns is the typed load's scope: the packages of every
// unserved symbol. A symbol carrying no package widens the scope to
// the whole tree, never narrows it away.
func (s *Served) childPatterns() []string {
	if len(s.unserved) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, sym := range s.unserved {
		pkg, ok := packageOf(sym)
		if !ok {
			return nil
		}
		if !seen[pkg] {
			seen[pkg] = true
			out = append(out, pkg)
		}
	}
	sort.Strings(out)
	return out
}

// openingCapture fingerprints every unserved subject under every
// selection before the child opens: the fingerprints a record may
// publish under, contemporaneous with the snapshot the child answers
// from. Unknown subjects are narrowed by gofresh's refusal; a fault
// leaves the selection uncaptured, so nothing publishes under it.
func (s *Served) openingCapture() {
	s.opening = map[string]map[gofresh.Subject]gofresh.Fingerprint{}
	keys := make([]string, 0, len(s.sels))
	for key := range s.sels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fps, ok := s.captureUnder(key, s.unserved, "opening")
		if ok {
			s.opening[key] = fps
		}
	}
}

// captureUnder builds a view over the symbols' subjects under one
// selection, narrowing the subjects the selected source does not
// declare, and captures their fingerprints.
func (s *Served) captureUnder(key string, symbols []string, phase string) (map[gofresh.Subject]gofresh.Fingerprint, bool) {
	engine, err := selectionEngine(s.ctx, s.dir, s.sels[key])
	if err != nil {
		s.degraded = append(s.degraded, fmt.Sprintf("%s %q: %v", phase, key, err))
		return nil, false
	}
	var subjects []gofresh.Subject
	for _, sym := range symbols {
		pkg, ok := packageOf(sym)
		if !ok {
			continue
		}
		subjects = append(subjects, gofresh.Subject{Package: pkg, Symbol: sym[len(pkg)+1:]})
	}
	var view *gofresh.View
	for len(subjects) > 0 {
		view, err = engine.NewView(s.ctx, subjects, s.dir)
		var unknown *gofresh.UnknownSubjectsError
		if errors.As(err, &unknown) {
			gone := map[gofresh.Subject]bool{}
			for _, subject := range unknown.Subjects {
				gone[subject] = true
			}
			if len(gone) == 0 {
				s.degraded = append(s.degraded, fmt.Sprintf("%s %q: %v", phase, key, err))
				return nil, false
			}
			kept := subjects[:0]
			for _, subject := range subjects {
				if !gone[subject] {
					kept = append(kept, subject)
				}
			}
			subjects = kept
			continue
		}
		if err != nil {
			s.degraded = append(s.degraded, fmt.Sprintf("%s %q: %v", phase, key, err))
			return nil, false
		}
		break
	}
	if len(subjects) == 0 {
		return map[gofresh.Subject]gofresh.Fingerprint{}, true
	}
	fps, err := view.CaptureBatch(s.ctx)
	if err != nil {
		s.degraded = append(s.degraded, fmt.Sprintf("%s %q: %v", phase, key, err))
		return nil, false
	}
	return fps, true
}

// serveSelection checks one selection's records: the recorded subjects
// viewed together, the ones the selected source no longer declares
// narrowed away by gofresh's own refusal, the rest batch-checked.
func (s *Served) serveSelection(key string, recs []resolutioncache.Record) {
	engine, err := selectionEngine(s.ctx, s.dir, s.sels[key])
	if err != nil {
		s.degraded = append(s.degraded, fmt.Sprintf("selection %q: %v", key, err))
		return
	}
	subjects := make([]gofresh.Subject, 0, len(recs))
	bySubject := map[gofresh.Subject]resolutioncache.Record{}
	for _, rec := range recs {
		subject, ok := recordSubject(rec)
		if !ok {
			continue
		}
		subjects = append(subjects, subject)
		bySubject[subject] = rec
	}
	var view *gofresh.View
	for len(subjects) > 0 {
		view, err = engine.NewView(s.ctx, subjects, s.dir)
		var unknown *gofresh.UnknownSubjectsError
		if errors.As(err, &unknown) {
			// Vanished since they were recorded: stale, resolved
			// typed, which answers not-found for them.
			gone := map[gofresh.Subject]bool{}
			for _, subject := range unknown.Subjects {
				gone[subject] = true
				s.reasons[bySubject[subject].Symbol] = "no longer declared in the selected source"
			}
			if len(gone) == 0 {
				s.degraded = append(s.degraded, fmt.Sprintf("selection %q: %v", key, err))
				return
			}
			kept := subjects[:0]
			for _, subject := range subjects {
				if !gone[subject] {
					kept = append(kept, subject)
				}
			}
			subjects = kept
			continue
		}
		if err != nil {
			s.degraded = append(s.degraded, fmt.Sprintf("selection %q: %v", key, err))
			return
		}
		break
	}
	if len(subjects) == 0 {
		return
	}
	// Equivalence is of the SOURCE closure alone: the current capture's
	// closure tiers — maximal closure, toolchain, build configuration —
	// must equal the record's. gofresh's result-serving verdict judges
	// more (dynamic state, purity, runtime inputs), which a resolution
	// never depends on, so it is not consulted here.
	current, err := view.CaptureBatch(s.ctx)
	if err != nil {
		s.degraded = append(s.degraded, fmt.Sprintf("selection %q: %v", key, err))
		return
	}
	for _, subject := range subjects {
		rec := bySubject[subject]
		fp, captured := current[subject]
		if !captured {
			s.reasons[rec.Symbol] = "no current capture"
			continue
		}
		if why := closureMoved(rec.Fingerprint, fp); why != "" {
			s.reasons[rec.Symbol] = why
			continue
		}
		s.served[rec.Symbol] = rec
	}
}

// closureMoved names the source-closure tier that differs between a
// record's fingerprint and the current capture, or "" when the
// closures are equal — the resolution's whole proof. The tiers are
// gofresh's source tiers in its order: the maximal (core) closure, the
// subject package's test-variant compartment — the core excludes the
// package's own test-only declarations, and a witness's class and
// serving refusal are functions of its own body, which lives exactly
// there — then the toolchain and the build configuration. An empty
// compartment digest on either side fails closed.
func closureMoved(recorded witnesscache.Fingerprint, current gofresh.Fingerprint) string {
	switch {
	case recorded.MaximalClosure != current.MaximalClosure:
		return "closure"
	case recorded.TestVariantClosure == "" || recorded.TestVariantClosure != current.TestVariantClosure:
		return "test variants"
	case recorded.Toolchain != current.Guards.Toolchain:
		return "toolchain"
	case recorded.BuildConfig != current.Guards.BuildConfig:
		return "build configuration"
	}
	return ""
}

// Notices is the served path's account for a result: one line for the
// served and typed counts, one per selection the path degraded, and
// one per typed symbol naming why its record did not serve — advisory,
// never a verdict input.
func (s *Served) Notices() []string {
	var out []string
	out = append(out, fmt.Sprintf("resolution: %d served from records, %d resolved typed", len(s.served), len(s.unserved)))
	for _, d := range s.degraded {
		out = append(out, "resolution degraded to typed: "+d)
	}
	keys := make([]string, 0, len(s.reasons))
	for k := range s.reasons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, fmt.Sprintf("resolution typed: %s: %s", k, s.reasons[k]))
	}
	return out
}

// Reasons names, per recorded symbol that resolved typed this run, why
// its record did not serve.
func (s *Served) Reasons() map[string]string {
	out := make(map[string]string, len(s.reasons))
	for k, v := range s.reasons {
		out[k] = v
	}
	return out
}

// recordSubject is the gofresh subject a record was captured for: the
// symbol's rest beneath its owning package.
func recordSubject(rec resolutioncache.Record) (gofresh.Subject, bool) {
	rest, ok := strings.CutPrefix(rec.Symbol, rec.Package+".")
	if !ok || rest == "" {
		return gofresh.Subject{}, false
	}
	return gofresh.Subject{Package: rec.Package, Symbol: rest}, true
}

// Degraded names the serving-path faults this run degraded to typed
// resolution — advisory, one line per selection.
func (s *Served) Degraded() []string { return append([]string(nil), s.degraded...) }

// ServedCount is the number of symbols answered from records this run.
func (s *Served) ServedCount() int { return len(s.served) }

func (s *Served) ensureChild() (*Owned, error) {
	if s.child == nil {
		// The opening capture precedes the child's snapshot: a record
		// publishes under a fingerprint no later than the answer it
		// carries.
		s.openingCapture()
		patterns := s.childPatterns()
		child, err := NewOwnedScoped(s.ctx, s.dir, patterns)
		if err != nil {
			return nil, err
		}
		s.child = child
	}
	return s.child, nil
}

// Slice implements verify.Slicer through the child: the
// declaration-reading roles have no served form.
func (s *Served) Slice(symbols []string) ([]verify.Decl, error) {
	child, err := s.ensureChild()
	if err != nil {
		return nil, err
	}
	return child.Slice(symbols)
}

// SliceFloor implements verify.FloorSlicer through the child.
func (s *Served) SliceFloor(symbols []string, declaredPkgs []string) ([]verify.FloorPackage, error) {
	child, err := s.ensureChild()
	if err != nil {
		return nil, err
	}
	return child.SliceFloor(symbols, declaredPkgs)
}

// typed resolves one symbol through the child, once per run.
func (s *Served) typed(symbol string) childAnswer {
	if a, ok := s.answers[symbol]; ok && a.resolved {
		return a
	}
	a := s.answers[symbol]
	a.resolved = true
	if !s.admits(symbol) {
		a.err = outsideSet(symbol)
		s.answers[symbol] = a
		return a
	}
	child, err := s.ensureChild()
	if err != nil {
		a.err = err
		s.answers[symbol] = a
		return a
	}
	res, shape, selection, err := child.ResolveIn(symbol)
	a.res, a.shape, a.selection, a.err = res, shape, selection, err
	if err == nil && res != verify.NotFound {
		if pkg, perr := child.SymbolPackage(symbol); perr == nil {
			a.pkg = pkg
		}
		if _, known := s.sels[selection]; known && a.pkg != "" {
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

// SymbolPackage implements verify.SymbolLocator.
func (s *Served) SymbolPackage(symbol string) (string, error) {
	if rec, ok := s.served[symbol]; ok {
		return rec.Package, nil
	}
	if a, ok := s.answers[symbol]; ok && a.err == nil && a.pkg != "" {
		return a.pkg, nil
	}
	if !s.admits(symbol) {
		return "", outsideSet(symbol)
	}
	child, err := s.ensureChild()
	if err != nil {
		return "", err
	}
	return child.SymbolPackage(symbol)
}

// admits reports whether the backend answers symbol: every symbol when
// the operation named no set (the child is the whole tree), else the
// set's own — the one admission every role consults, so the boundary
// cannot drift between them.
func (s *Served) admits(symbol string) bool {
	if _, ok := packageOf(symbol); !ok {
		// No package to scope by: the child answers not found for it
		// under any load, never a narrowed frontier's not found.
		return true
	}
	return len(s.set) == 0 || s.set[symbol]
}

// outsideSet is the refusal for a symbol the operation never named:
// the child is scoped to the set's stale remainder, so forwarding an
// outside symbol would answer not found for what the tree declares.
func outsideSet(symbol string) error {
	return fmt.Errorf("symbol %q is outside this operation's symbol set", symbol)
}

// classify answers a symbol's witness class and reason through the
// child, once per run.
func (s *Served) classify(symbol string) (verify.WitnessClass, string) {
	a := s.answers[symbol]
	if a.classed {
		return a.class, a.reason
	}
	if !s.admits(symbol) {
		return verify.ExampleWitness, outsideSet(symbol).Error()
	}
	child, err := s.ensureChild()
	if err != nil {
		return verify.ExampleWitness, ""
	}
	class, reason := child.WitnessClassVerdict(symbol)
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

// NeverServe implements verify.WitnessSeeding: served records answer
// their recorded refusal; the rest ask the child in one batch.
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
	if len(ask) == 0 {
		return out, nil
	}
	child, err := s.ensureChild()
	if err != nil {
		return nil, err
	}
	// A witness classified typed is resolved typed too, so its record
	// — refusal included — publishes at close and the next run's
	// classification serves.
	for _, symbol := range ask {
		s.typed(symbol)
	}
	refusals, err := child.NeverServe(ask)
	if err != nil {
		return nil, err
	}
	for symbol, why := range refusals {
		out[symbol] = why
	}
	s.refusals = mergeRefusals(s.refusals, ask, refusals)
	return out, nil
}

// Close publishes a record for every symbol the child resolved this run
// whose subject gofresh can fingerprint — the typed answer, its class,
// its serving refusal — and closes the child. Records are the next run's
// served set; a publication fault leaves the run's answers untouched.
func (s *Served) Close() error {
	defer func() {
		if s.child != nil {
			s.child.Close()
		}
	}()
	if s.child == nil || len(s.pending) == 0 {
		return nil
	}
	keys := make([]string, 0, len(s.pending))
	for key := range s.pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		s.publishSelection(key, s.pending[key])
	}
	return nil
}

func (s *Served) publishSelection(key string, symbols []string) {
	// Every symbol's class and refusal, from the child, before the
	// capture: the record carries the whole answer set.
	var unclassed []string
	for _, symbol := range symbols {
		if !s.answers[symbol].classed {
			unclassed = append(unclassed, symbol)
		}
	}
	for _, symbol := range unclassed {
		s.classify(symbol)
	}
	if s.refusals == nil {
		s.refusals = map[string]string{}
	}
	var unrefused []string
	for _, symbol := range symbols {
		if _, asked := s.refusals[symbol]; !asked {
			unrefused = append(unrefused, symbol)
		}
	}
	if len(unrefused) > 0 {
		if refusals, err := s.child.NeverServe(unrefused); err == nil {
			s.refusals = mergeRefusals(s.refusals, unrefused, refusals)
		} else {
			return
		}
	}
	closing, ok := s.captureUnder(key, symbols, "publish")
	if !ok {
		return
	}
	opening := s.opening[key]
	var recs []resolutioncache.Record
	moved := 0
	for _, symbol := range symbols {
		a := s.answers[symbol]
		pkg, ok := packageOf(symbol)
		if !ok {
			continue
		}
		subject := gofresh.Subject{Package: pkg, Symbol: symbol[len(pkg)+1:]}
		fp, captured := closing[subject]
		if !captured {
			continue
		}
		// The straddle check: the tree the child answered from is the
		// tree the record's fingerprint describes only when the closing
		// capture equals the opening one; a subject that moved between
		// them resolves typed again next run.
		before, opened := opening[subject]
		if !opened || closureMoved(witnesscache.FromGofresh(before), fp) != "" {
			moved++
			continue
		}
		recs = append(recs, resolutioncache.Record{
			Selection: key, Symbol: symbol, Fingerprint: witnesscache.FromGofresh(before),
			Resolution: resolutionWire(a.res), Shape: a.shape, Package: a.pkg,
			WitnessClass: classWire(a.class), WitnessClassReason: a.reason,
			NeverServe: s.refusals[symbol],
		})
	}
	if err := resolutioncache.InstallAll(s.dir, recs); err != nil {
		s.degraded = append(s.degraded, fmt.Sprintf("publish %q: %v", key, err))
	}
}

// mergeRefusals records, for every asked symbol, the child's refusal or
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

// WitnessSymbols names every witness subject of the captured policy —
// "package.Test" for each capture group's subjects — the symbols the
// witness run classifies, so an operation's served backend is prepared
// over them beside the bound symbols.
func WitnessSymbols(ctx context.Context, pc *Capture) ([]string, error) {
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
		if witnesses, err = WitnessSymbols(ctx, pc); err != nil {
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
// subject of the captured policy carries (REQ-evidence-store-gc). A
// policy the operation cannot capture keeps every witness record —
// cost cleanup never guesses.
func GCResolutions(ctx context.Context, dir string, store *records.Store, pc *Capture) (removed, kept int, err error) {
	symbols, err := OperationSymbols(ctx, store, pc)
	if err != nil {
		return 0, 0, err
	}
	live := make(map[string]bool, len(symbols))
	for _, sym := range symbols {
		live[sym] = true
	}
	return resolutioncache.GC(dir, func(_, symbol string) bool { return live[symbol] })
}
