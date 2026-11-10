package golang

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"github.com/greatliontech/go-x-tools/go/packages"
	"github.com/greatliontech/stipulator/internal/canon"
	"github.com/greatliontech/stipulator/internal/verify"
)

// structuralPkg is the analyzer-assertion library: a test invoking it is
// the proof class.
const structuralPkg = "github.com/greatliontech/stipulator/stipulate/structural"

func structuralAssertion(name string) bool {
	switch name {
	case "ImportAllowlist", "NoImport", "Implements", "ExportedData", "ExportedDataWithMethods", "FunctionSignature":
		return true
	default:
		return false
	}
}

// rapidPkg is the recognized property-test library: a test driving its
// check runner quantifies over generated inputs. Generator construction
// alone does not quantify, so only the drivers classify.
const rapidPkg = "pgregory.net/rapid"

// gopterPkg is the third recognized property library: its check driver
// is the Properties.TestingRun method - generator construction and
// Property registration alone do not quantify (REQ-go-witness-class).
const gopterPkg = "github.com/leanovate/gopter"

func rapidDriver(name string) bool { return name == "Check" || name == "MakeCheck" }

// quickPkg is the standard library's property driver: Check and
// CheckEqual quantify a callback over inputs drawn from Config.Rand —
// the wall clock's seed when nil — so a body driving either is
// random-seeded; Value alone generates and does not classify
// (REQ-go-witness-class).
const quickPkg = "testing/quick"

func quickDriver(name string) bool { return name == "Check" || name == "CheckEqual" }

// WitnessClass implements verify.WitnessClassifier: a test invoking the
// structural library yields an analyzer proof; a fuzz target — a function
// taking *testing.F — or a test driving a rapid check runner (a qualified
// or aliased rapid.Check / rapid.MakeCheck selector call in its own body)
// yields a property witness; everything else — including dot-imported
// driver calls — is an example witness. Resolved from the code, never
// declared.
func (b *Backend) WitnessClass(symbol string) verify.WitnessClass {
	class, _ := b.WitnessClassVerdict(symbol)
	return class
}

// WitnessClassVerdict implements verify.WitnessClassVerdicts: the class
// plus, for an example classification, the verdict naming what the
// bound body lacks - a recognized library referenced without its
// classifying call is named exactly, so a property test misclassified
// by helper indirection is diagnosed from the row, never by
// trial-and-error edits.
func (b *Backend) WitnessClassVerdict(symbol string) (verify.WitnessClass, string) {
	v := b.classifyWitness(symbol)
	return v.class, v.reason
}

// seededReason is the serving refusal a random-seeded witness carries
// wherever a served or published record is refused: the uncacheable set
// and the re-execution reasons alike (REQ-evidence-witness-freshness-seeded).
var seededReason = reasonSeeded.with(": executes every run, never served")

// seededThroughReason is the serving refusal of a witness whose bound body
// reaches a run-time-seeded driver only through in-module helpers: the
// evidence classification stays example (REQ-go-witness-class is direct-call
// by contract), but the executed quantification draws from a run-time seed
// exactly as a direct driver's does, so serving refuses it under a reason
// naming the first hop (REQ-evidence-witness-freshness-seeded).
func seededThroughReason(helper string) uncacheable {
	return reasonSeeded.with(" through " + helper + ": executes every run, never served")
}

// NeverServe implements verify.WitnessSeeding: the symbols whose
// witness classification is property by a run-time-seeded driver —
// rapid.Check / rapid.MakeCheck, gopter's Properties.TestingRun — carry
// seededReason; never a fuzz target, whose ordinary run replays its
// committed seeds deterministically (REQ-go-witness-class). A symbol
// the loaded views cannot classify joins the set under its own reason
// naming the load gap: with no body to inspect there is no proof of a
// deterministic quantification, and absence of proof never serves
// (REQ-evidence-witness-freshness-seeded) — but the refusal must never read
// as a property classification the code does not carry.
func (b *Backend) NeverServe(symbols []string) (map[string]string, error) {
	out := map[string]string{}
	for _, s := range symbols {
		switch v := b.classifyWitness(s); {
		case v.seeded:
			out[s] = seededReason.String()
		case v.seededVia != "":
			out[s] = seededThroughReason(v.seededVia).String()
		case v.seedingRefusal != "":
			// An in-module callee whose package would not load: no
			// declaration to walk, so no proof the quantification is
			// deterministic — absence of proof never serves.
			out[s] = v.seedingRefusal
		case !v.inspected:
			out[s] = reasonUnclassifiable.with(v.reason).String()
		}
	}
	return out, nil
}

// witnessVerdict is one bound symbol's resolved classification: the
// class, the example-classification reason, whether a property
// classification is random-seeded, and whether its source supplies a
// classification judgment (REQ-go-witness-class). A runnable fuzz entry
// derives its property class from the harness even without a Go body.
type witnessVerdict struct {
	class     verify.WitnessClass
	reason    string
	seeded    bool
	inspected bool
	// seededVia names the first in-module helper through which the
	// bound body reaches a run-time-seeded driver when no direct call
	// classifies it: the transitive seeding class serving consults,
	// distinct from the evidence class (REQ-evidence-witness-freshness-seeded).
	seededVia string
	// seedingRefusal is the serving refusal the walk raises when an
	// in-module callee's package cannot be loaded: the walk has no
	// declaration to read, so serving fails closed under this reason.
	seedingRefusal string
}

// callTarget is the callee expression of a call with a generic
// instantiation's index unwrapped: structural.Implements[io.Reader](t, x)
// and run[int](t, body) are still direct calls of their function.
func callTarget(call *ast.CallExpr) ast.Expr {
	switch idx := call.Fun.(type) {
	case *ast.IndexExpr:
		return idx.X
	case *ast.IndexListExpr:
		return idx.X
	}
	return call.Fun
}

// driverCall reports whether a call expression directly drives a
// run-time-seeded property runner — a qualified or aliased rapid.Check
// / rapid.MakeCheck, gopter's Properties.TestingRun, or testing/quick's
// Check / CheckEqual — the one spelling the direct classification and
// the transitive seeding walk share.
func driverCall(pkg *packages.Package, call *ast.CallExpr) bool {
	sel, ok := callTarget(call).(*ast.SelectorExpr)
	return ok && driverSelector(pkg, sel)
}

// driverSelector reports whether a selector names a run-time-seeded
// property driver — called, or named as a value the body may call.
func driverSelector(pkg *packages.Package, sel *ast.SelectorExpr) bool {
	return driverObject(pkg.TypesInfo.Uses[sel.Sel])
}

// recognizedLibrary names the recognized library an object belongs to
// — rapid, gopter, testing/quick, or the structural analyzer — and ""
// for every other object; the one membership test the classification's
// reference and dot-import arms and the driver test read.
func recognizedLibrary(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	switch p := obj.Pkg().Path(); p {
	case rapidPkg, gopterPkg, quickPkg, structuralPkg:
		return p
	}
	return ""
}

// driverObject reports whether an object is a run-time-seeded property
// driver: rapid's Check / MakeCheck, gopter's TestingRun, quick's Check
// / CheckEqual — whatever spelling reaches it (a qualified selector, a
// dot-imported bare identifier, a value).
func driverObject(obj types.Object) bool {
	switch recognizedLibrary(obj) {
	case rapidPkg:
		return rapidDriver(obj.Name())
	case gopterPkg:
		return obj.Name() == "TestingRun"
	case quickPkg:
		return quickDriver(obj.Name())
	}
	return false
}

// walkKey joins a build selection and a declaration's stable name: the
// key every seeding-walk memo uses, so tag-split declarations never
// collapse across views.
func walkKey(sel string, fn *types.Func) string { return sel + "\x00" + declKey(fn) }

// initWalk creates the seeding walk's memos on first use and indexes
// the loaded declarations under their own views. The caller holds
// walkMu.
func (b *Backend) initWalk() {
	if b.declIndex != nil {
		return
	}
	b.declIndex = map[string]declaredFunc{}
	b.lazyLoaded = map[string]error{}
	if b.notInModule == nil {
		b.notInModule = map[string]bool{}
	}
	b.bodies = map[string]bodyFacts{}
	for _, pkg := range b.pkgs {
		b.indexDecls(b.selectionOf(pkg), []*packages.Package{pkg})
	}
}

// selectionOf is the build selection a package of the construction's
// loads was produced under; a package loaded on demand is not in the
// load index and carries its selection on its declarations instead.
func (b *Backend) selectionOf(pkg *packages.Package) string {
	return b.loadSelection[b.load[pkg]]
}

// indexDecls adds every function declaration of successfully loaded packages to the
// declaration index under the selection, keyed on the declared object
// — the origin an instantiated method resolves back to. The caller
// holds walkMu.
func (b *Backend) indexDecls(sel string, pkgs []*packages.Package) {
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			continue
		}
		for _, f := range pkg.Syntax {
			for _, decl := range f.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok {
					if obj, ok := pkg.TypesInfo.Defs[fd.Name].(*types.Func); ok {
						b.declIndex[walkKey(sel, obj)] = declaredFunc{fd: fd, pkg: pkg, sel: sel}
					}
				}
			}
		}
	}
}

// inModule reports the workspace member whose module owns the package
// path, or "" for a dependency's package; the dependency verdict is
// memoized, since every standard and third-party callee asks it.
func (b *Backend) inModule(sel, pkgPath string) string {
	if b.notInModule == nil {
		b.notInModule = map[string]bool{}
	}
	key := sel + "\x00" + pkgPath
	if b.notInModule[key] {
		return ""
	}
	for m, patterns := range memberPatterns(b.selectionModules(sel), []string{pkgPath}) {
		if len(patterns) > 0 {
			return m
		}
	}
	b.notInModule[key] = true
	return ""
}

// funcDeclOf finds the declaration of a function object in one build
// selection's view: among the loaded packages' syntax, or — for an
// in-module package the scoped load did not hold — by loading that
// package on demand under its member's configuration for that
// selection and indexing it, so the seeding walk answers the same
// whatever the load's scope. A dependency's function has no declaration
// to find, so the walk stops where the module does (nil, no error); an
// in-module package that fails to load, or an in-module function whose
// declaration the selection's view does not carry after its package
// loaded, is an error the walk fails closed on. The caller holds walkMu.
func (b *Backend) funcDeclOf(sel string, fn *types.Func) (*ast.FuncDecl, *packages.Package, error) {
	fn = fn.Origin()
	b.initWalk()
	if fn.Pkg() == nil {
		return nil, nil, nil
	}
	pkgPath := fn.Pkg().Path()
	loadKey := sel + "\x00" + pkgPath
	// A retained failure precedes the declaration memo. Eager partial types
	// may expose a callable even though its package's source did not load;
	// use that exact type universe, not an unrelated variant or selection.
	if err := b.lazyLoaded[loadKey]; err != nil {
		return nil, nil, err
	}
	if owner := b.typesPkg[fn.Pkg()]; owner != nil && b.selectionOf(owner) == sel {
		if err := helperPackageError(owner, b.exportFailures[owner]); err != nil {
			return nil, nil, err
		}
	}
	key := walkKey(sel, fn)
	if d, ok := b.declIndex[key]; ok {
		return d.fd, d.pkg, nil
	}
	member := b.inModule(sel, pkgPath)
	if member == "" {
		return nil, nil, nil
	}
	missing := func() error {
		return fmt.Errorf("declaration of %s is not among the parsed files of in-module package %s in the %q view%s", fn.FullName(), pkgPath, viewLabel(sel), b.placedAt(fn))
	}
	if _, done := b.lazyLoaded[loadKey]; done {
		return nil, nil, missing()
	}
	if b.packageLoaded(sel, pkgPath) {
		// An imported export object can belong to another load's type
		// universe. A failed selected source load still supplies its own
		// diagnostic rather than pretending that the helper is absent.
		for _, pkg := range b.byPath[pkgPath] {
			if b.selectionOf(pkg) == sel {
				if err := helperPackageError(pkg, b.exportFailures[pkg]); err != nil {
					return nil, nil, err
				}
			}
		}
		// The package is held by this view's loads and its parsed
		// files declare no such function. The object and the index
		// read one parse under one selection, so no loaded view
		// produces this today (an abstract method, once the one
		// producer, is outside the walk); the arm stays fail-closed —
		// a nil here would read as a dependency's function and serve.
		return nil, nil, missing()
	}
	cfg := b.lazyCfg[member][sel]
	if cfg == nil {
		err := fmt.Errorf("in-module package %s: no load configuration for member %q under the %q view", pkgPath, member, sel)
		b.lazyLoaded[loadKey] = err
		return nil, nil, err
	}
	loaded, failures, err := loadTypedPackages(cfg, pkgPath)
	if err == nil {
		for _, pkg := range loaded {
			if err = helperPackageError(pkg, failures[pkg]); err != nil {
				break
			}
		}
	} else {
		err = fmt.Errorf("in-module package %s: %w", pkgPath, err)
	}
	if err != nil {
		// A cancellation is the caller's, never the package's: it is
		// not remembered against the path.
		if cfg.Context == nil || cfg.Context.Err() == nil {
			b.lazyLoaded[loadKey] = err
		}
		return nil, nil, err
	}
	b.lazyLoaded[loadKey] = nil
	b.indexDecls(sel, loaded)
	if d, ok := b.declIndex[key]; ok {
		return d.fd, d.pkg, nil
	}
	return nil, nil, missing()
}

// helperPackageError refuses partial helper bodies independently of whether
// the loader failure has an established disposable-export classification.
func helperPackageError(pkg *packages.Package, failure *missingExportError) error {
	if len(pkg.Errors) == 0 {
		return nil
	}
	if failure != nil {
		return fmt.Errorf("in-module package %s has load errors: %w", pkg.PkgPath, failure)
	}
	return fmt.Errorf("in-module package %s: %v", pkg.PkgPath, pkg.Errors[0])
}

// placedAt names where the type information declares a function —
// " (the type information places it at file:line)" — when the
// function's type universe is one of the construction's loads; a
// function of a universe the construction does not hold (an export-data
// import, an on-demand load) is placed nowhere and the suffix is empty.
func (b *Backend) placedAt(fn *types.Func) string {
	owner := b.typesPkg[fn.Pkg()]
	if owner == nil || !fn.Pos().IsValid() {
		return ""
	}
	return " (the type information places it at " + b.site(owner, fn.Pos()) + ")"
}

// inTreeRel reports whether a filepath.Rel result stays inside the base
// directory without mistaking an in-tree "..foo" component for a parent
// traversal.
func inTreeRel(rel string) bool {
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !strings.HasPrefix(rel, "../")
}

// site renders a position of a loaded package's file set as a
// tree-relative file:line — the spelling every chain link and every
// refusal carries; a position outside the tree keeps its own path.
func (b *Backend) site(pkg *packages.Package, pos token.Pos) string {
	p := pkg.Fset.PositionFor(pos, false)
	name := p.Filename
	if rel, err := filepath.Rel(b.dir, name); err == nil && inTreeRel(rel) {
		name = filepath.ToSlash(rel)
	}
	return fmt.Sprintf("%s:%d", name, p.Line)
}

// viewLabel spells a build selection's key for a reader: the default
// view by its name, otherwise the declared tags, toolchain and module mode.
func viewLabel(sel string) string {
	if sel == (buildSelection{}).key() {
		return sel
	}
	tags, rest, _ := strings.Cut(sel, "\x00")
	toolchain, rest, _ := strings.Cut(rest, "\x00")
	mode, rest, _ := strings.Cut(rest, "\x00")
	root, workspace, _ := strings.Cut(rest, "\x00")
	var parts []string
	if tags != "" {
		parts = append(parts, "-tags="+tags)
	}
	if toolchain != "" {
		parts = append(parts, "toolchain "+toolchain)
	}
	if mode != "" {
		parts = append(parts, mode)
	}
	if root != "" {
		parts = append(parts, "module-root "+root)
	}
	if workspace != "" {
		parts = append(parts, "workspace "+workspace)
	}
	return strings.Join(parts, " ")
}

// packageLoaded reports whether the selection's view already holds the
// package among the construction's loads.
func (b *Backend) packageLoaded(sel, pkgPath string) bool {
	for _, pkg := range b.byPath[pkgPath] {
		if b.selectionOf(pkg) == sel {
			return true
		}
	}
	return false
}

// dispatchReceiver reports whether a selector's receiver expression
// is a typed interface value — a dispatch outside the walk. A type
// parameter's receiver names the constraint's method, resolved to no
// declaration until instantiation (a pointer to a type parameter has
// no methods, so no unwrap), and a receiver the type information left
// untyped resolves nothing either: both refuse as the clause's default
// says, never a silent serve.
func dispatchReceiver(pkg *packages.Package, x ast.Expr) bool {
	tv, ok := pkg.TypesInfo.Types[x]
	if !ok || tv.Type == nil {
		return false
	}
	_, typeParam := tv.Type.(*types.TypeParam)
	return !typeParam
}

// bodyFactsOf answers a declared function's body facts — its direct
// classification signals, whether it drives or names a run-time-seeded
// runner, where, and its static callees — in one AST pass memoized per
// selection and function; the caller holds walkMu.
func (b *Backend) bodyFactsOf(sel string, fn *types.Func, fd *ast.FuncDecl, pkg *packages.Package) bodyFacts {
	if fn == nil {
		return b.scanBody(fd.Body, pkg)
	}
	key := walkKey(sel, fn)
	if facts, ok := b.bodies[key]; ok {
		return facts
	}
	facts := b.scanBody(fd.Body, pkg)
	b.bodies[key] = facts
	return facts
}

// scanBody derives every classification and seeding fact of a function
// body in one AST walk: direct proof and property calls, near-miss
// library references, the first driver call or reference site in any
// spelling, and the resolved static callees and unresolved calls.
func (b *Backend) scanBody(body ast.Node, pkg *packages.Package) bodyFacts {
	var facts bodyFacts
	if body == nil {
		return facts
	}
	handled := map[*ast.Ident]bool{}
	resolve := func(id *ast.Ident, sel *ast.SelectorExpr, site string, call bool) {
		switch obj := pkg.TypesInfo.Uses[id].(type) {
		case *types.Func:
			if sig, ok := obj.Type().(*types.Signature); ok && sig.Recv() != nil && types.IsInterface(sig.Recv().Type()) {
				if sel == nil || !dispatchReceiver(pkg, sel.X) {
					facts.callees.unresolved = append(facts.callees.unresolved, calleeRef{name: id.Name, site: site})
				}
				return
			}
			facts.callees.callees = append(facts.callees.callees, calleeRef{fn: obj.Origin(), site: site})
		case nil:
			if call && pkg.TypesInfo.Defs[id] == nil {
				facts.callees.unresolved = append(facts.callees.unresolved, calleeRef{name: id.Name, site: site})
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, ok := callTarget(x).(*ast.SelectorExpr); ok {
				if obj := pkg.TypesInfo.Uses[sel.Sel]; obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == structuralPkg && structuralAssertion(obj.Name()) {
					facts.proof = true
				}
			}
			if driverCall(pkg, x) {
				facts.property = true
				if facts.driverSite == "" {
					facts.driverSite = b.site(pkg, x.Lparen)
				}
			}
			var id *ast.Ident
			var sel *ast.SelectorExpr
			switch f := callTarget(x).(type) {
			case *ast.Ident:
				id = f
			case *ast.SelectorExpr:
				id, sel = f.Sel, f
			default:
				return true
			}
			handled[id] = true
			resolve(id, sel, b.site(pkg, x.Lparen), true)
		case *ast.SelectorExpr:
			switch recognizedLibrary(pkg.TypesInfo.Uses[x.Sel]) {
			case rapidPkg:
				facts.rapidRef = true
			case structuralPkg:
				facts.structuralRef = true
			case gopterPkg:
				facts.gopterRef = true
			case quickPkg:
				facts.quickRef = true
			}
			if facts.driverSite == "" && driverSelector(pkg, x) {
				facts.driverSite = b.site(pkg, x.Sel.Pos())
			}
			if !handled[x.Sel] {
				handled[x.Sel] = true
				resolve(x.Sel, x, b.site(pkg, x.Sel.Pos()), false)
			}
		case *ast.Ident:
			if recognizedLibrary(pkg.TypesInfo.Uses[x]) != "" {
				facts.dotImported = true
			}
			if facts.driverSite == "" && driverObject(pkg.TypesInfo.Uses[x]) {
				facts.driverSite = b.site(pkg, x.Pos())
			}
			if !handled[x] {
				handled[x] = true
				resolve(x, nil, b.site(pkg, x.Pos()), false)
			}
		}
		return true
	})
	facts.drives = facts.driverSite != ""
	return facts
}

// staticCallees returns a body's resolved static callees and unresolved
// calls from the shared one-pass body scan.
func (b *Backend) staticCallees(body ast.Node, pkg *packages.Package) walkedBody {
	return b.scanBody(body, pkg).callees
}

// seededRefusal is the fail-closed serving refusal the walk raises where
// it has no declaration to read.
func seededRefusal(err error) uncacheable {
	return reasonSeedingRefused.with(err.Error())
}

// seedingHop is one edge of the walk's path: a body's call of an
// in-module callee at the call's site (the caller nil for the bound
// body itself).
type seedingHop struct {
	caller *types.Func
	callee *types.Func
	site   string
}

// seedingPath is one walk's answer for a body under one selection:
// the first hop's name when a helper drives a runner (via, the reason's
// spelling), the hops from the body to that helper and the driving
// call's site in its body; else the refusal — the first unresolved
// call or unreadable declaration met in breadth-first order — with the
// body holding the refusing call (nil for a body with no object), the
// call's site, and the hops into that body. A hop found outranks a
// refusal met before it: the walk's hop return clears the refusal, so
// a path never carries both.
type seedingPath struct {
	via         string
	hops        []seedingHop
	driverSite  string
	refusal     string
	refusalIn   *types.Func
	refusalHops []seedingHop
	refusalSite string
}

// seededThrough walks the static in-module callees of a bound body
// under the body's own build selection — breadth first, each frame
// remembering the frame that reached it, a seen set ending cycles — to
// the first helper whose own body directly drives a run-time-seeded
// runner, returning the path to it. A dependency callee ends its
// branch. An in-module callee whose declaration the walk cannot read,
// or a call the type information cannot resolve, is a refusal serving
// fails closed on; the walk still finishes, and a hop found outranks
// the refusal as the reason. The walk answers serving's question (does
// the executed quantification draw from a run-time seed?), never the
// evidence class; the explain derivation renders the same path.
func (b *Backend) seededThrough(sel string, rootFn *types.Func, fd *ast.FuncDecl, pkg *packages.Package) seedingPath {
	b.walkMu.Lock()
	defer b.walkMu.Unlock()
	b.initWalk()
	type frame struct {
		fn     *types.Func
		caller *types.Func
		site   string
		parent int
	}
	var frames []frame
	hopsTo := func(i int) []seedingHop {
		var hops []seedingHop
		for ; i >= 0; i = frames[i].parent {
			f := frames[i]
			hops = append(hops, seedingHop{caller: f.caller, callee: f.fn, site: f.site})
		}
		slices.Reverse(hops)
		return hops
	}
	var path seedingPath
	refuse := func(err error, in *types.Func, hops []seedingHop, site string) {
		if path.refusal == "" {
			path.refusal, path.refusalIn, path.refusalHops, path.refusalSite = seededRefusal(err).String(), in, hops, site
		}
	}
	seen := map[string]bool{}
	var queue []int
	// The root's own callees come from the per-function memo where
	// the root is a declared function (every witness is); a body with
	// no object is resolved once here.
	var root walkedBody
	if rootFn != nil {
		root = b.bodyFactsOf(sel, rootFn, fd, pkg).callees
	} else {
		root = b.staticCallees(fd.Body, pkg)
	}
	if len(root.unresolved) > 0 {
		refuse(fmt.Errorf("call of %s in the bound body resolves to no declaration", root.unresolved[0].name), rootFn, nil, root.unresolved[0].site)
	}
	for _, c := range root.callees {
		if k := declKey(c.fn); !seen[k] {
			seen[k] = true
			frames = append(frames, frame{fn: c.fn, caller: rootFn, site: c.site, parent: -1})
			queue = append(queue, len(frames)-1)
		}
	}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		f := frames[i]
		fd, fpkg, err := b.funcDeclOf(sel, f.fn)
		if err != nil {
			// The refusing call is the caller's call of this callee:
			// the hops run into the caller, the site is that call's.
			refuse(err, f.caller, hopsTo(f.parent), f.site)
			continue
		}
		if fd == nil || fd.Body == nil {
			continue
		}
		facts := b.bodyFactsOf(sel, f.fn, fd, fpkg)
		if facts.drives {
			path.hops = hopsTo(i)
			path.via = path.hops[0].callee.FullName()
			path.driverSite = facts.driverSite
			path.refusal, path.refusalIn, path.refusalHops, path.refusalSite = "", nil, nil, ""
			return path
		}
		if len(facts.callees.unresolved) > 0 {
			u := facts.callees.unresolved[0]
			refuse(fmt.Errorf("call of %s in %s resolves to no declaration", u.name, f.fn.FullName()), f.fn, hopsTo(i), u.site)
		}
		for _, c := range facts.callees.callees {
			if k := declKey(c.fn); !seen[k] {
				seen[k] = true
				frames = append(frames, frame{fn: c.fn, caller: f.fn, site: c.site, parent: i})
				queue = append(queue, len(frames)-1)
			}
		}
	}
	return path
}

func (b *Backend) classifyWitness(symbol string) witnessVerdict {
	// Proof outranks property, property outranks example: resolved from
	// the body's callees. Only a test the witness run executes can
	// classify above example — a structural or rapid invocation in a
	// plain function never runs.
	fd, pkg, err := b.funcDecl(symbol)
	if err == nil && fd.Body != nil && runnableWitness(fd, pkg) {
		sel := b.selectionOf(pkg)
		root, _ := pkg.TypesInfo.Defs[fd.Name].(*types.Func)
		b.walkMu.Lock()
		b.initWalk()
		facts := b.bodyFactsOf(sel, root, fd, pkg)
		b.walkMu.Unlock()
		proof, property := facts.proof, facts.property
		rapidRef, structuralRef, gopterRef, quickRef, dotImported := facts.rapidRef, facts.structuralRef, facts.gopterRef, facts.quickRef, facts.dotImported
		// Serving is one answer for the symbol across every view that
		// holds it — a record serves only the view that produced it,
		// but the refusal is a property of the symbol — so the seeding
		// walk runs under each view's own selection before the class
		// is decided, and every inspected verdict carries its answer:
		// a proof or fuzz classification in this view does not serve a
		// symbol another view drives (fail-closed union).
		// A plain property witness is seeded by its own body and its
		// verdict reads no union. A proof body's verdict reads the
		// union's fields whatever else its body does, so the union is
		// computed for it even when it also drives — a belt keeping the
		// proof arm's inputs defined however its seeding is composed.
		var direct bool
		var via, refusal string
		if !property || proof {
			s := b.seededInAnyView(symbol, b.selectionOf(pkg), fd, pkg)
			direct, via, refusal = s.direct, s.path.via, s.path.refusal
		}
		switch {
		case proof:
			// Proof outranks property on the ladder; a proof body that
			// also drives a runner is random-seeded all the same.
			return witnessVerdict{class: verify.AnalyzerProof, inspected: true, seeded: property || direct, seededVia: via, seedingRefusal: refusal}
		case property:
			// Driver-quantified: the driver draws the inputs from a
			// run-time seed, so the witness is random-seeded.
			return witnessVerdict{class: verify.PropertyWitness, seeded: true, inspected: true}
		}
		// A fuzz target quantifies by its harness whatever its body
		// calls - the signature check below classifies it property. Its
		// ordinary run replays the committed seeds: deterministic, never
		// random-seeded — unless another view's body, or a helper it
		// reaches, drives a runner.
		if witnessHandle(fd, pkg) == "F" {
			return witnessVerdict{class: verify.PropertyWitness, inspected: true, seeded: direct, seededVia: via, seedingRefusal: refusal}
		}
		// An example verdict carries the transitive seeding class too:
		// a driver reached only through in-module helpers classifies
		// example on the evidence ladder and refuses serving, its
		// near-miss reason naming the hop the walk found.
		example := func(reason string) witnessVerdict {
			if via != "" {
				reason += " (reached through " + via + directCallRemedy + ")"
			}
			// A direct driver call in another view's body makes the
			// symbol random-seeded outright; the class stays this
			// view's, serving refuses under the direct spelling.
			return witnessVerdict{class: verify.ExampleWitness, reason: reason, inspected: true, seeded: direct, seededVia: via, seedingRefusal: refusal}
		}
		switch {
		case rapidRef:
			return example("rapid.Check not invoked in the bound body")
		case gopterRef:
			return example("gopter.Properties.TestingRun not invoked in the bound body")
		case quickRef:
			return example("testing/quick.Check not invoked in the bound body")
		case structuralRef:
			return example("no structural assertion invoked in the bound body")
		case dotImported:
			return example("recognized library reached through a dot import - only a qualified call classifies")
		default:
			return example("no property driver or analyzer call in the bound body")
		}
	}
	// A runnable fuzz declaration may be implemented outside its Go body
	// (for example by a linked function). Its harness still quantifies it;
	// the physical entry-point checks apply even on this bodiless path.
	if err == nil && runnableWitness(fd, pkg) {
		if witnessHandle(fd, pkg) == "F" {
			return witnessVerdict{class: verify.PropertyWitness, inspected: true}
		}
		return witnessVerdict{class: verify.ExampleWitness, reason: "no Go body to inspect"}
	}
	// Classification is resolved from the code (REQ-go-witness-class);
	// a body that cannot even load has no code to resolve from, so the
	// verdict names the load failure — the dependency-resolution
	// attribution riding Resolve's error — never a class derived from
	// the body's absence (REQ-go-load-attribution).
	if _, _, err := b.Resolve(symbol); err != nil {
		return witnessVerdict{class: verify.ExampleWitness, reason: err.Error()}
	}
	return witnessVerdict{class: verify.ExampleWitness, reason: "not a runnable test witness"}
}

// directCallRemedy closes a near-miss reason that names a hop: the
// evidence tier is direct-call by contract, so the remedy keeps the
// driver call in the bound body — a completion guard rides as the test
// context the driver is handed, never as the caller of the driver
// (REQ-go-witness-class).
const directCallRemedy = "; the tier is direct-call by contract — drive the runner in the bound body, a guard riding as its test context"

// seeding is serving's answer for a symbol across its loaded views: a
// direct driver call in another view's body (at its site, under that
// view), else the walk's path in the first view that seeds, else the
// first refusal met, the path empty when nothing seeds — with the
// selection that answered and, where another view answered, the
// witness's declaration site in that view.
type seeding struct {
	direct      bool
	directSite  string
	sel         string
	witnessSite string
	path        seedingPath
}

// seededInAnyView answers serving's one question for the symbol across
// every loaded view: the resolved view's body is walked (its direct
// driver call was classified already); every other view declaring the
// symbol has its own body tested for a direct driver call first —
// direct there, the symbol is random-seeded outright — then walked.
// A hop in any view seeds; a refusal in any view, a declaration of
// another view that cannot be read included, refuses.
func (b *Backend) seededInAnyView(symbol string, sel string, fd *ast.FuncDecl, pkg *packages.Package) seeding {
	root, _ := pkg.TypesInfo.Defs[fd.Name].(*types.Func)
	if root != nil {
		// The resolved body naming a driver in any spelling — a
		// qualified call (classified already), a value, a dot-imported
		// bare identifier — seeds it directly for serving; the
		// classification stays direct-call by contract.
		b.walkMu.Lock()
		facts := b.bodyFactsOf(sel, root, fd, pkg)
		b.walkMu.Unlock()
		if facts.drives {
			return seeding{direct: true, directSite: facts.driverSite, sel: sel}
		}
	}
	answer := seeding{sel: sel, path: b.seededThrough(sel, root, fd, pkg)}
	if answer.path.via != "" {
		return answer
	}
	if b.singleView(symbol) {
		// One view holds the symbol: the resolved walk is the union.
		return answer
	}
	others, otherRefusal := b.declsInOtherViews(symbol, sel)
	if answer.path.refusal == "" {
		answer.path.refusal = otherRefusal
	}
	for _, other := range others {
		b.walkMu.Lock()
		facts := b.bodyFactsOf(other.sel, other.fn, other.fd, other.pkg)
		b.walkMu.Unlock()
		at := b.site(other.pkg, other.fd.Name.Pos())
		if facts.drives {
			return seeding{direct: true, directSite: facts.driverSite, sel: other.sel, witnessSite: at}
		}
		p := b.seededThrough(other.sel, other.fn, other.fd, other.pkg)
		if p.via != "" {
			return seeding{sel: other.sel, path: p, witnessSite: at}
		}
		if answer.path.refusal == "" && p.refusal != "" {
			// The refusing view answers: its refusal, its selection,
			// its declaration of the witness.
			answer.path, answer.sel, answer.witnessSite = p, other.sel, at
		}
	}
	return answer
}

// singleView reports whether every loaded package holding the symbol's
// path was produced under one selection — the common tree, whose
// union is the resolved walk alone.
func (b *Backend) singleView(symbol string) bool {
	pkgPath, _ := b.splitSymbol(symbol)
	seen := ""
	for _, bucket := range [][]*packages.Package{b.byPath[pkgPath], b.byPath[pkgPath+"_test"]} {
		for _, pkg := range bucket {
			sel := b.selectionOf(pkg)
			if seen == "" {
				seen = sel
			} else if sel != seen {
				return false
			}
		}
	}
	return true
}

// declsInOtherViews finds the symbol's declaration in every loaded view
// other than the resolved one, once per view — a package and its test
// variant hold one declaration; a view whose declaration cannot be read
// is a refusal, never a view dropped from the union (a belt: a symbol
// the view's lookup resolved has its declaration in that view).
func (b *Backend) declsInOtherViews(symbol string, resolvedSel string) ([]declaredFunc, string) {
	pkgPath, rest := b.splitSymbol(symbol)
	if pkgPath == "" {
		return nil, ""
	}
	parts := strings.Split(rest, ".")
	var out []declaredFunc
	refusal := ""
	seen := map[string]bool{}
	for _, bucket := range [][]*packages.Package{b.byPath[pkgPath], b.byPath[pkgPath+"_test"]} {
		for _, pkg := range bucket {
			sel := b.selectionOf(pkg)
			if sel == resolvedSel {
				continue
			}
			fn, ok := lookup(pkg.Types, parts).(*types.Func)
			if !ok || seen[walkKey(sel, fn)] {
				continue
			}
			seen[walkKey(sel, fn)] = true
			b.walkMu.Lock()
			fd, fpkg, err := b.funcDeclOf(sel, fn)
			b.walkMu.Unlock()
			switch {
			case err != nil:
				if refusal == "" {
					refusal = seededRefusal(fmt.Errorf("%s in the %q view: %w", symbol, sel, err)).String()
				}
			case fd != nil && fd.Body != nil:
				out = append(out, declaredFunc{fd: fd, pkg: fpkg, sel: sel, fn: fn})
			}
		}
	}
	return out, refusal
}

// runnableWitness reports whether the declaration is a test the ordinary
// witness run executes: a Test or Fuzz function in a _test.go file taking
// the matching testing handle, per go test's naming rule (the name after
// the prefix must not start lowercase). Anything else never runs, so it
// can never produce evidence.
func runnableWitness(fd *ast.FuncDecl, pkg *packages.Package) bool {
	return witnessHandle(fd, pkg) != ""
}

// witnessHandle joins the harness's syntax with the resolved testing type.
// A diagnostic //line name cannot turn production code into a test file.
func witnessHandle(fd *ast.FuncDecl, pkg *packages.Package) string {
	var handle string
	switch {
	case syntacticTestFunc(fd, "Test", "T"):
		handle = "T"
	case syntacticTestFunc(fd, "Fuzz", "F"):
		handle = "F"
	default:
		return ""
	}
	if !strings.HasSuffix(pkg.Fset.PositionFor(fd.Pos(), false).Filename, "_test.go") {
		return ""
	}
	fn, ok := pkg.TypesInfo.Defs[fd.Name].(*types.Func)
	if !ok {
		return ""
	}
	sig := fn.Type().(*types.Signature)
	if sig.Recv() != nil || sig.Params().Len() != 1 {
		return ""
	}
	ptr, ok := sig.Params().At(0).Type().(*types.Pointer)
	if !ok {
		return ""
	}
	named, ok := types.Unalias(ptr.Elem()).(*types.Named)
	if !ok {
		return ""
	}
	obj := named.Obj()
	if obj.Pkg() != nil && obj.Pkg().Path() == "testing" && obj.Name() == handle {
		return handle
	}
	return ""
}

// shapeHash hashes the object's declared type rendered with fully
// qualified package paths.
func shapeHash(obj types.Object) string {
	return canon.Hash(types.ObjectString(obj, func(p *types.Package) string {
		return p.Path()
	}))
}
