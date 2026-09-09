// Package golang is the Go language backend: it resolves Go symbol
// references through the type checker and hashes their declared shapes.
//
// A symbol reference is "<import-path>.<Ident>" or, for methods,
// "<import-path>.<Receiver>.<Method>". The import path is matched against
// loaded package paths (longest match), never parsed lexically, so import
// paths containing dots resolve correctly. Kind and shape are resolved from
// the code, never declared in the reference.
package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"golang.org/x/mod/modfile"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/packages"

	"github.com/greatliontech/stipulator/internal/policy"

	"github.com/greatliontech/stipulator/internal/canon"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Backend resolves symbols within one Go tree: a single module, or a
// workspace whose go.work members are all in scope.
type Backend struct {
	pkgs []*packages.Package
	// generatedHeaders memoizes the generated-file verdict of declaring
	// files outside the loads, by path.
	generatedHeaders sync.Map
	// viewErrors names tagged views whose load failed whole (a broken
	// or absent selection toolchain): binding stays healthy for every
	// symbol the loaded views resolve, while a reference the loaded
	// views cannot answer refuses with the degraded views named -
	// never a silent NotFound that masks an unloadable view
	// (REQ-go-build-selections).
	viewErrors []string
	// load labels each package with the packages.Load call that
	// produced it: one load per (workspace member, build selection),
	// each with its own token.FileSet, so position containment is only
	// meaningful between packages of the same load
	// (REQ-go-build-selections).
	load map[*packages.Package]int
	// loadSelection names each load's build selection — the key a
	// served resolution record carries, so a record serves only the
	// view that produced it (REQ-evidence-resolution-freshness).
	loadSelection map[int]string
	// dir is the absolute tree root New loaded, kept to reconcile
	// Fset-absolute file paths back to the tree-relative paths the corpus
	// and the git layer speak in.
	dir string
	// pinsOnce/pinTable lazily derive the committed dependency-pin
	// surface for load-failure attribution — error paths only, so a
	// healthy resolution never pays the parse.
	pinsOnce sync.Once
	pinTable *pinTable
	// members are the workspace members newContext loaded (the module
	// alone, or every go.work member); with lazyCfg, one load
	// configuration per member under the default selection, they let
	// the seeding walk load an in-module callee's package the scoped
	// load did not hold, so the walk answers the same whatever the
	// load's scope (REQ-evidence-witness-freshness).
	members []string
	// modules maps each member to its module path, read once.
	modules map[string]string
	// lazyCfg holds one load configuration per member and build
	// selection, captured for every member at construction — a member
	// owning no scoped pattern included — and consulted under the
	// walking witness's own selection, so a tag-split helper is read in
	// the view the witness runs in. Its context is the resolver's own.
	lazyCfg map[string]map[string]*packages.Config
	// walkMu guards the seeding walk's memos, all keyed by build
	// selection and the declaration's stable name (declKey): declIndex
	// maps each loaded function to its declaration, extended as lazy
	// loads land; lazyLoaded records each on-demand package load per
	// selection with its error (a cancellation is never recorded);
	// notInModule memoizes the dependency verdict per package path;
	// bodyDrives and bodyCallees memoize, per declared function,
	// whether its own body directly drives a runner and the static
	// callees it resolves. The lock is held across an on-demand go
	// list: the walk is serialized, and a load must not race the
	// index it extends.
	walkMu      sync.Mutex
	declIndex   map[string]declaredFunc
	lazyLoaded  map[string]error
	notInModule map[string]bool
	bodyDrives  map[string]bool
	bodyCallees map[string]walkedBody
	// typesPkg maps each loaded package's type universe to the package,
	// so an object's own view is recoverable; byPath maps each package
	// path to the loaded packages holding it across views and variants.
	typesPkg map[*types.Package]*packages.Package
	byPath   map[string][]*packages.Package
}

// declaredFunc is one loaded declaration with its package and the
// build selection it was read under, the index value funcDeclOf
// serves — the selection travels with the declaration so a package
// loaded on demand never has to be asked which view produced it.
type declaredFunc struct {
	fd  *ast.FuncDecl
	pkg *packages.Package
	sel string
	// fn is the declared object, set where the declaration was found
	// through a symbol lookup rather than the index.
	fn *types.Func
}

// walkedBody is one declared body's memoized resolution: the static
// callees the type information resolves and the calls it could not.
type walkedBody struct {
	callees    []*types.Func
	unresolved []string
}

// declKey names a function object stably across loads: a package
// loaded on demand lives in its own type universe, so object identity
// cannot join a callee seen in one load to its declaration read in
// another — the declared origin's package path and full name can.
func declKey(fn *types.Func) string {
	fn = fn.Origin()
	if fn.Pkg() == nil {
		return fn.FullName()
	}
	return fn.Pkg().Path() + "|" + fn.FullName()
}

// pins derives the committed pin surface once per backend.
func (b *Backend) pins() *pinTable {
	b.pinsOnce.Do(func() { b.pinTable = loadPinTable(b.dir) })
	return b.pinTable
}

// newContext loads the tree rooted at dir, including test packages: the
// module alone, or every go.work member when the tree is a workspace —
// package patterns are module-scoped, so nested published modules would
// otherwise vanish from symbol resolution. A load failure is an error:
// per the spec, an unloadable tree is a verification error, never an
// absence. Deliberately unexported: in-process loading spawns go list
// outside any owned process group, so the only cross-package door to
// package discovery is the owned resolver client (NewWholeTree — the
// client alone), keeping
// REQ-go-owned-processes structurally satisfied for every consumer.
// newContext loads the tree's resolution views: every workspace member
// under every build selection, over "./..." — or, when patterns are
// given, over exactly those packages, the scope a served resolution's
// stale remainder needs (REQ-evidence-resolution-freshness). Their
// dependencies stay export data; the one verdict that reads a
// dependency's source — a promoted method's declaring file, for the
// generated-file marker — reads that file's header itself
// (generatedIn), so a scoped load answers as the whole-tree load does.
func newContext(ctx context.Context, dir string, patterns []string) (*Backend, error) {
	members, err := workspaceMembers(dir)
	if err != nil {
		return nil, err
	}
	env, err := goworkEnv(dir)
	if err != nil {
		return nil, err
	}
	selections, crossPlatform, err := policyBuildSelections(dir)
	if err != nil {
		return nil, err
	}
	var pkgs []*packages.Package
	var viewErrors []string
	// Cross-platform selections are named refusals, not silent
	// absences: a reference the loaded views cannot answer refuses
	// with the unresolvable selection named, exactly as an unloadable
	// view does.
	for _, cp := range crossPlatform {
		viewErrors = append(viewErrors, cp+" (no on-host resolution view)")
	}
	loadIndex := map[*packages.Package]int{}
	loadSelection := map[int]string{}
	loads := 0
	// Pattern ownership is selection-independent: each member's go.mod
	// is read once, not once per build selection.
	modules := memberModules(dir, members)
	owned := memberPatterns(modules, patterns)
	lazyCfg := map[string]map[string]*packages.Config{}
	for _, sel := range selections {
		viewEnv := selectionViewEnv(env, sel)
		// The selection view is a frontend parse of the selection's own
		// sources, so it inherits the toolchain-provenance prerequisite:
		// an identified selection toolchain this binary's frontend
		// cannot read refuses the run
		// (REQ-evidence-toolchain-provenance). A toolchain that cannot
		// even be sampled loads no view — that case falls through to
		// the per-view unloadable degradation below
		// (REQ-go-build-selections).
		if err := checkToolchainSkewIdentified(dir, viewEnv); err != nil {
			return nil, err
		}
		var viewPkgs []*packages.Package
		viewLoads := map[*packages.Package]int{}
		viewFailed := false
		for _, m := range members {
			cfg := &packages.Config{
				Context: ctx,
				Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
					packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports |
					packages.NeedEmbedFiles,
				Dir:   filepath.Join(dir, m),
				Env:   viewEnv,
				Tests: true,
			}
			cfg.BuildFlags = selectionViewFlags(sel)
			// Every member's configuration under every selection is kept
			// for the seeding walk's on-demand loads of in-module helper
			// packages — a member owning no scoped pattern included: a
			// helper is importable code, so its package loads without
			// its test variants.
			lazy := *cfg
			lazy.Tests = false
			if lazyCfg[m] == nil {
				lazyCfg[m] = map[string]*packages.Config{}
			}
			lazyCfg[m][SelectionKey(sel.tags, sel.toolchain)] = &lazy
			load := []string{"./..."}
			if len(patterns) > 0 {
				// A pattern loads from the one member whose module
				// owns it: loading every member over the same import
				// paths would hold each package once per member.
				load = owned[m]
				if len(load) == 0 {
					continue
				}
			}
			loaded, err := packages.Load(cfg, load...)
			if err == nil && len(patterns) > 0 {
				loaded = matchedRoots(loaded)
			}
			if err != nil {
				if len(sel.tags) == 0 {
					return nil, fmt.Errorf("loading Go packages in %s: %w", m, err)
				}
				// A tagged view that cannot load degrades to a named
				// refusal instead of failing the whole binding context:
				// the pre-existing views' binding health is the floor,
				// and the view's own symbols refuse with this reason.
				viewErrors = append(viewErrors, fmt.Sprintf("build selection -tags=%s (toolchain %q): loading Go packages in %s: %v", strings.Join(sel.tags, ","), sel.toolchain, m, err))
				viewFailed = true
				break
			}
			for _, pkg := range loaded {
				viewLoads[pkg] = loads
			}
			loadSelection[loads] = SelectionKey(sel.tags, sel.toolchain)
			loads++
			viewPkgs = append(viewPkgs, loaded...)
		}
		if viewFailed {
			continue
		}
		for pkg, load := range viewLoads {
			loadIndex[pkg] = load
		}
		// Deterministic candidate order within a view; views concatenate
		// in selection order - default first, then the policy's tagged
		// views - so resolution's first-declaring-view rule falls out of
		// plain iteration (REQ-go-build-selections).
		sort.Slice(viewPkgs, func(i, j int) bool { return viewPkgs[i].ID < viewPkgs[j].ID })
		pkgs = append(pkgs, viewPkgs...)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving tree root %s: %w", dir, err)
	}
	typesPkg := map[*types.Package]*packages.Package{}
	byPath := map[string][]*packages.Package{}
	for _, pkg := range pkgs {
		if pkg.Types != nil {
			typesPkg[pkg.Types] = pkg
		}
		byPath[pkg.PkgPath] = append(byPath[pkg.PkgPath], pkg)
	}
	return &Backend{pkgs: pkgs, load: loadIndex, loadSelection: loadSelection, viewErrors: viewErrors, dir: abs, members: members, modules: modules, lazyCfg: lazyCfg, typesPkg: typesPkg, byPath: byPath}, nil
}

// matchedRoots drops the roots a scoped load's patterns matched nothing
// for: go list answers a vanished package's path with an error entry
// carrying no Go files, which "./..." never enumerates, so the scoped
// load must not hold it either — its symbols answer not found, as the
// whole-tree load answers, never a load error.
func matchedRoots(loaded []*packages.Package) []*packages.Package {
	kept := loaded[:0]
	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 && len(pkg.GoFiles) == 0 {
			continue
		}
		kept = append(kept, pkg)
	}
	return kept
}

// memberModules reads each workspace member's module path once — the
// ownership table memberPatterns and the seeding walk's in-module
// judgment both consult.
func memberModules(dir string, members []string) map[string]string {
	modules := map[string]string{}
	for _, m := range members {
		data, err := os.ReadFile(filepath.Join(dir, m, "go.mod"))
		if err != nil {
			continue
		}
		if path := modfile.ModulePath(data); path != "" {
			modules[m] = path
		}
	}
	return modules
}

// memberPatterns assigns each import-path pattern to the workspace
// member whose module path is its longest prefix. A pattern no member's
// module owns is loaded by nobody: under a workspace go list would
// resolve it out of the module graph as a root — a dependency's
// package, typed and shaped — where the whole-tree load answers not
// found, and the scoped load must answer as the whole tree does.
func memberPatterns(modules map[string]string, patterns []string) map[string][]string {
	out := map[string][]string{}
	if len(patterns) == 0 {
		return out
	}
	for _, pattern := range patterns {
		owner, longest := "", -1
		for m, path := range modules {
			if (pattern == path || strings.HasPrefix(pattern, path+"/")) && len(path) > longest {
				owner, longest = m, len(path)
			}
		}
		if owner != "" {
			out[owner] = append(out[owner], pattern)
		}
	}
	return out
}

// selectionViewEnv is the environment a selection's view loads under:
// the tree's workspace pin, and the selection's own toolchain when it
// declares one — exactly as its invocation executes, since a
// toolchain'd tag view under the ambient toolchain would miss the
// selection's own stdlib surface (REQ-go-build-selections). The one
// derivation the resolver child's typed views and the served
// resolution's freshness views share, so a record's fingerprint is
// checked under the environment that produced it.
func selectionViewEnv(env []string, sel buildSelection) []string {
	if sel.toolchain == "" {
		return env
	}
	return append(dropEnv(append([]string(nil), env...), "GOTOOLCHAIN"), "GOTOOLCHAIN="+sel.toolchain)
}

// selectionViewFlags is the build selection's package-load flags: its
// effective tag set, the race tag included as a tag (the views load
// sources, they never instrument).
func selectionViewFlags(sel buildSelection) []string {
	if len(sel.tags) == 0 {
		return nil
	}
	return []string{"-tags=" + strings.Join(sel.tags, ",")}
}

// SelectionKey is a build selection's stable key — its effective tag
// set and toolchain — the coordinate a resolution record serves under.
// The default selection (no tags, ambient toolchain) is "default".
func SelectionKey(tags []string, toolchain string) string {
	if len(tags) == 0 && toolchain == "" {
		return "default"
	}
	return strings.Join(tags, ",") + "\x00" + toolchain
}

// policyBuildSelections derives the resolution views from the accepted
// policy record: the default no-tag view first, then one view per
// distinct invocation tag-set in the policy's canonical invocation
// order (REQ-go-build-selections). The policy is the authority on
// which build selections exist - execution discovery already runs
// them - so resolution reads the same record rather than growing a
// configuration surface. A tree without a record resolves the default
// view alone; a malformed record is a verification error, never a
// silent narrowing.
// buildSelection is one resolution view's identity: the invocation's
// effective tag-set (declared tags plus the implicit `race` tag of a
// -race invocation) and its toolchain - a view is the pair, never the
// tags alone (REQ-go-build-selections).
type buildSelection struct {
	tags      []string
	toolchain string
}

func policyBuildSelections(dir string) ([]buildSelection, []string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(policy.Path)))
	if os.IsNotExist(err) {
		return []buildSelection{{}}, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading the accepted policy for resolution build selections: %w", err)
	}
	p, err := policy.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("resolution build selections: %w", err)
	}
	selections := []buildSelection{{}}
	seen := map[string]bool{}
	var crossPlatform []string
	for _, inv := range p.GetInvocations() {
		cfg := inv.GetGo()
		if cfg == nil {
			continue
		}
		// A cross-platform selection (declared GOOS/GOARCH differing
		// from the host) cannot execute on-host, so a resolution-only
		// view would bind witnesses no run can grant; it is refused by
		// name rather than resolved silently — the on-host resolution
		// design for that dimension is its own chunk of work. A
		// declared value equal to the host is the host view.
		if (cfg.HasGoos() && cfg.GetGoos() != runtime.GOOS) ||
			(cfg.HasGoarch() && cfg.GetGoarch() != runtime.GOARCH) {
			crossPlatform = append(crossPlatform,
				fmt.Sprintf("invocation %q declares GOOS/GOARCH %q/%q off the %s/%s host",
					inv.GetName(), cfg.GetGoos(), cfg.GetGoarch(), runtime.GOOS, runtime.GOARCH))
			continue
		}
		tags := append([]string(nil), cfg.GetTags()...)
		if cfg.GetRace() {
			// The -race build sets the implicit race tag: a
			// //go:build race declaration resolves in exactly the
			// views whose invocations would compile it.
			tags = append(tags, "race")
		}
		// The effective set is canonical (sorted, deduplicated): a
		// declared literal "race" tag beside race:true is one
		// selection, never a duplicate view's load cost.
		sort.Strings(tags)
		tags = slices.Compact(tags)
		if len(tags) == 0 {
			continue
		}
		toolchain := cfg.GetToolchain()
		key := strings.Join(tags, ",") + "\x00" + toolchain
		if seen[key] {
			continue
		}
		seen[key] = true
		selections = append(selections, buildSelection{tags: tags, toolchain: toolchain})
	}
	return selections, crossPlatform, nil
}

// Resolve implements verify.Backend.
func (b *Backend) Resolve(symbol string) (verify.Resolution, string, error) {
	res, shape, _, err := b.ResolveIn(symbol)
	return res, shape, err
}

// ResolveIn is Resolve naming the build selection whose view resolved
// the symbol (SelectionKey), empty when it did not resolve: the
// coordinate a resolution record is served under.
func (b *Backend) ResolveIn(symbol string) (verify.Resolution, string, string, error) {
	// The degraded-view refusal precedes every silent-NotFound arm: a
	// reference the loaded views cannot even prefix-match could live in
	// the unloadable view, and the amended contract forbids a silent
	// absence that masks one (REQ-go-build-selections).
	notFound := func() (verify.Resolution, string, string, error) {
		if len(b.viewErrors) > 0 {
			return verify.NotFound, "", "", fmt.Errorf("symbol unresolved and %d build-selection view(s) failed to load: %s", len(b.viewErrors), strings.Join(b.viewErrors, "; "))
		}
		return verify.NotFound, "", "", nil
	}
	pkgPath, rest := b.splitSymbol(symbol)
	if pkgPath == "" {
		return notFound()
	}
	parts := strings.Split(rest, ".")
	if len(parts) == 0 || len(parts) > 2 {
		return notFound()
	}
	for _, pkg := range b.pkgs {
		if pkg.PkgPath != pkgPath && pkg.PkgPath != pkgPath+"_test" {
			continue
		}
		if len(pkg.Errors) > 0 {
			// A dependency-rooted failure names the import, its module,
			// and the committed directives that pin it, so the
			// operator's next step is a decision, not a diagnosis; an
			// in-tree failure surfaces the loader's diagnostic
			// unchanged (REQ-go-load-attribution).
			if msg, ok := b.loadErrorAttribution(pkg); ok {
				return verify.NotFound, "", "", fmt.Errorf("package %s: %s", pkg.ID, msg)
			}
			return verify.NotFound, "", "", fmt.Errorf("package %s has load errors: %v", pkg.ID, pkg.Errors[0])
		}
		obj := lookup(pkg.Types, parts)
		if obj == nil {
			continue
		}
		if b.generatedIn(pkg, obj) {
			return verify.GeneratedFile, "", b.loadSelection[b.load[pkg]], nil
		}
		return verify.Resolved, shapeHash(obj), b.loadSelection[b.load[pkg]], nil
	}
	return notFound()
}

// SymbolFile returns the tree-relative, slash-separated path of the file
// declaring the symbol, and false when the symbol does not resolve or its
// declaration lies outside the loaded tree (a method promoted from an
// out-of-tree embedded type declares elsewhere). Best-effort by
// contract: a degraded build-selection view yields false here - the
// advisory impact preview omits the binding - while Resolve refuses
// loudly for the same reference, which is what bind and verify consult.
func (b *Backend) SymbolFile(symbol string) (string, bool) {
	pkgPath, rest := b.splitSymbol(symbol)
	if pkgPath == "" {
		return "", false
	}
	parts := strings.Split(rest, ".")
	if len(parts) == 0 || len(parts) > 2 {
		return "", false
	}
	for _, pkg := range b.pkgs {
		if pkg.PkgPath != pkgPath && pkg.PkgPath != pkgPath+"_test" {
			continue
		}
		obj := lookup(pkg.Types, parts)
		if obj == nil || !obj.Pos().IsValid() {
			continue
		}
		rel, err := filepath.Rel(b.dir, pkg.Fset.Position(obj.Pos()).Filename)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", false
		}
		return filepath.ToSlash(rel), true
	}
	return "", false
}

// SymbolPackage returns the loaded package path owning the symbol
// reference (verify.SymbolLocator) — external test variants folded onto
// their production path — or "" when no loaded package matches. The one
// source for package-scoped correlation: a symbol string alone cannot
// be split reliably (dotted path elements vs method receivers).
func (b *Backend) SymbolPackage(symbol string) string {
	p, _ := b.splitSymbol(symbol)
	return p
}

// ReachedPackages returns the packages — named by production import path,
// test variants folded in — that the given tree-relative files reach
// through the reverse import graph: the packages the files belong to,
// plus every package importing one of those, transitively. A file a
// package embeds at compile time seeds it exactly like a source file.
// Paths not
// belonging to any loaded package contribute nothing; reach through
// non-import couplings (runtime inputs, generated artifacts) is invisible
// here by construction, which is why an impact preview is advisory.
func (b *Backend) ReachedPackages(files []string) map[string]bool {
	inFile := make(map[string]bool, len(files))
	for _, f := range files {
		inFile[f] = true
	}
	rev := map[string][]string{}
	seeds := map[string]bool{}
	for _, pkg := range b.pkgs {
		np := variantBase(pkg)
		for _, imp := range pkg.Imports {
			// Without NeedDeps an out-of-tree import is a stub whose only
			// identity is its ID; in-tree imports share the fully loaded
			// root nodes. Either way the folded build identity is the
			// edge key - never a path-spelling trim, which misfolds a
			// real package whose import path ends in "_test".
			target := variantBase(imp)
			rev[target] = append(rev[target], np)
		}
		// EmbedFiles seed exactly like source files: an embed is a
		// compile-time input the loader names, so an asset edit reaches
		// its embedding package and everything importing it.
		for _, list := range [][]string{pkg.GoFiles, pkg.OtherFiles, pkg.EmbedFiles} {
			for _, f := range list {
				rel, err := filepath.Rel(b.dir, f)
				if err != nil {
					continue
				}
				if inFile[filepath.ToSlash(rel)] {
					seeds[np] = true
				}
			}
		}
	}
	reached := map[string]bool{}
	var walk func(string)
	walk = func(p string) {
		if reached[p] {
			return
		}
		reached[p] = true
		for _, q := range rev[p] {
			walk(q)
		}
	}
	for s := range seeds {
		walk(s)
	}
	return reached
}

// splitSymbol finds the loaded package whose path prefixes the symbol
// (longest match wins) and returns it with the remainder.
func (b *Backend) splitSymbol(symbol string) (string, string) {
	best := ""
	for _, pkg := range b.pkgs {
		p := variantBase(pkg)
		if strings.HasPrefix(symbol, p+".") && len(p) > len(best) {
			best = p
		}
	}
	if best == "" {
		return "", ""
	}
	return best, strings.TrimPrefix(symbol, best+".")
}

// lookup finds a package-scope object, or a method through its receiver
// type name.
func lookup(pkg *types.Package, parts []string) types.Object {
	obj := pkg.Scope().Lookup(parts[0])
	if obj == nil {
		return nil
	}
	if len(parts) == 1 {
		return obj
	}
	tn, ok := obj.(*types.TypeName)
	if !ok {
		return nil
	}
	// The pointer method set includes both pointer- and value-receiver
	// methods — but is empty for interface types, so fall back to the
	// value method set.
	for _, ms := range []*types.MethodSet{
		types.NewMethodSet(types.NewPointer(tn.Type())),
		types.NewMethodSet(tn.Type()),
	} {
		for i := 0; i < ms.Len(); i++ {
			// The method set includes PROMOTED methods: T.M resolves
			// to the method Go's own selector semantics denote, the
			// declared method of the embedded type. That is deliberate
			// — the shape pin is taken over the DECLARED object, so if
			// T later declares its own M the resolved shape changes
			// and the pin stales into re-consent; the retarget is
			// never silent. Generated-code detection likewise follows
			// the declaring file (a hand-written wrapper never
			// launders a generated method). The admission is exactly
			// as wide as the method set: an embedded INTERFACE's
			// method resolves (bodiless — no FuncDecl matches it, so
			// it degrades to not-a-runnable-witness downstream), and
			// a method promoted from an embedded FOREIGN type
			// resolves to its foreign declaration, whose
			// package-qualified path rides the shape hash.
			if m := ms.At(i).Obj(); m.Name() == parts[1] {
				return m
			}
		}
	}
	return nil
}

// structuralPkg is the analyzer-assertion library: a test invoking it is
// the proof class.
const structuralPkg = "github.com/greatliontech/stipulator/stipulate/structural"

func structuralAssertion(name string) bool {
	switch name {
	case "ImportAllowlist", "NoImport", "Implements", "ExportedData", "FunctionSignature":
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
// and the re-execution reasons alike (REQ-evidence-witness-freshness).
const seededReason = "random-seeded property witness: executes every run, never served"

// seededThroughReason is the serving refusal of a witness whose bound
// body reaches a run-time-seeded driver only through in-module helpers:
// the evidence classification stays example (REQ-go-witness-class is
// direct-call by contract), but the executed quantification draws from a
// run-time seed exactly as a direct driver's does, so serving refuses
// it under a reason naming the first hop (REQ-evidence-witness-freshness).
func seededThroughReason(helper string) string {
	return "random-seeded property witness through " + helper + ": executes every run, never served"
}

// NeverServe implements verify.WitnessSeeding: the symbols whose
// witness classification is property by a run-time-seeded driver —
// rapid.Check / rapid.MakeCheck, gopter's Properties.TestingRun — carry
// seededReason; never a fuzz target, whose ordinary run replays its
// committed seeds deterministically (REQ-go-witness-class). A symbol
// the loaded views cannot classify joins the set under its own reason
// naming the load gap: with no body to inspect there is no proof of a
// deterministic quantification, and absence of proof never serves
// (REQ-evidence-witness-freshness) — but the refusal must never read
// as a property classification the code does not carry.
func (b *Backend) NeverServe(symbols []string) (map[string]string, error) {
	out := map[string]string{}
	for _, s := range symbols {
		switch v := b.classifyWitness(s); {
		case v.seeded:
			out[s] = seededReason
		case v.seededVia != "":
			out[s] = seededThroughReason(v.seededVia)
		case v.seedingRefusal != "":
			// An in-module callee whose package would not load: no
			// declaration to walk, so no proof the quantification is
			// deterministic — absence of proof never serves.
			out[s] = v.seedingRefusal
		case !v.inspected:
			out[s] = "unclassifiable witness: executes every run, never served (absence of proof never serves): " + v.reason
		}
	}
	return out, nil
}

// witnessVerdict is one bound symbol's resolved classification: the
// class, the example-classification reason, whether a property
// classification is random-seeded, and whether a runnable body was
// inspected at all (REQ-go-witness-class).
type witnessVerdict struct {
	class     verify.WitnessClass
	reason    string
	seeded    bool
	inspected bool
	// seededVia names the first in-module helper through which the
	// bound body reaches a run-time-seeded driver when no direct call
	// classifies it: the transitive seeding class serving consults,
	// distinct from the evidence class (REQ-evidence-witness-freshness).
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
// / rapid.MakeCheck or gopter's Properties.TestingRun — the one spelling
// the direct classification and the transitive seeding walk share.
func driverCall(pkg *packages.Package, call *ast.CallExpr) bool {
	sel, ok := callTarget(call).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	obj := pkg.TypesInfo.Uses[sel.Sel]
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	switch obj.Pkg().Path() {
	case rapidPkg:
		return rapidDriver(sel.Sel.Name)
	case gopterPkg:
		return sel.Sel.Name == "TestingRun"
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
	b.bodyDrives = map[string]bool{}
	b.bodyCallees = map[string]walkedBody{}
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

// indexDecls adds every function declaration of the packages to the
// declaration index under the selection, keyed on the declared object
// — the origin an instantiated method resolves back to. The caller
// holds walkMu.
func (b *Backend) indexDecls(sel string, pkgs []*packages.Package) {
	for _, pkg := range pkgs {
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
func (b *Backend) inModule(pkgPath string) string {
	if b.notInModule == nil {
		b.notInModule = map[string]bool{}
	}
	if b.notInModule[pkgPath] {
		return ""
	}
	for m, patterns := range memberPatterns(b.modules, []string{pkgPath}) {
		if len(patterns) > 0 {
			return m
		}
	}
	b.notInModule[pkgPath] = true
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
	key := walkKey(sel, fn)
	if d, ok := b.declIndex[key]; ok {
		return d.fd, d.pkg, nil
	}
	if fn.Pkg() == nil {
		return nil, nil, nil
	}
	pkgPath := fn.Pkg().Path()
	member := b.inModule(pkgPath)
	if member == "" {
		return nil, nil, nil
	}
	missing := func() error {
		return fmt.Errorf("declaration of %s is not in the %q view of in-module package %s", fn.FullName(), sel, pkgPath)
	}
	loadKey := sel + "\x00" + pkgPath
	if err, done := b.lazyLoaded[loadKey]; done {
		if err != nil {
			return nil, nil, err
		}
		if d, ok := b.declIndex[key]; ok {
			return d.fd, d.pkg, nil
		}
		return nil, nil, missing()
	}
	if b.packageLoaded(sel, pkgPath) {
		// The package is in this view already and holds no such
		// declaration: a tag-excluded file, or generated code the view
		// does not carry — no declaration to read, so no proof.
		return nil, nil, missing()
	}
	cfg := b.lazyCfg[member][sel]
	if cfg == nil {
		err := fmt.Errorf("in-module package %s: no load configuration for member %q under the %q view", pkgPath, member, sel)
		b.lazyLoaded[loadKey] = err
		return nil, nil, err
	}
	loaded, err := packages.Load(cfg, pkgPath)
	if err == nil {
		for _, pkg := range loaded {
			if len(pkg.Errors) > 0 && len(pkg.GoFiles) == 0 {
				err = fmt.Errorf("in-module package %s: %v", pkgPath, pkg.Errors[0])
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

// staticCallees are the function objects a body's calls resolve
// through the type information — a plain or qualified identifier, a
// method selector, a generic instantiation unwrapped — each to its
// declared origin, with the calls the type information could not
// resolve named beside them: an unresolved callee is a body whose
// reach is unknown. A function value called, a value passed to be
// called elsewhere, and an interface method dispatch resolve to no
// declaration here.
func staticCallees(body ast.Node, pkg *packages.Package) walkedBody {
	var out walkedBody
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var id *ast.Ident
		switch f := callTarget(call).(type) {
		case *ast.Ident:
			id = f
		case *ast.SelectorExpr:
			id = f.Sel
		default:
			return true
		}
		switch obj := pkg.TypesInfo.Uses[id].(type) {
		case *types.Func:
			out.callees = append(out.callees, obj.Origin())
		case nil:
			if pkg.TypesInfo.Defs[id] == nil {
				out.unresolved = append(out.unresolved, id.Name)
			}
		}
		return true
	})
	return out
}

// bodyDrivesRunner reports whether a declared function's own body
// directly drives a run-time-seeded runner, memoized per selection and
// function with the body's static callees; the caller holds walkMu.
func (b *Backend) bodyDrivesRunner(sel string, fn *types.Func, fd *ast.FuncDecl, pkg *packages.Package) bool {
	key := walkKey(sel, fn)
	if drives, ok := b.bodyDrives[key]; ok {
		return drives
	}
	drives := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if drives {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && driverCall(pkg, call) {
			drives = true
			return false
		}
		return true
	})
	b.bodyDrives[key] = drives
	b.bodyCallees[key] = staticCallees(fd.Body, pkg)
	return drives
}

// seededRefusal is the fail-closed serving refusal the walk raises where
// it has no declaration to read.
func seededRefusal(err error) string {
	return "unclassifiable seeding: executes every run, never served (absence of proof never serves): " + err.Error()
}

// seededThrough walks the static in-module callees of a bound body
// under the body's own build selection — breadth first, the first hop's
// name carried down its branch, a seen set ending cycles — to the first
// helper whose own body directly drives a run-time-seeded runner,
// returning that hop's name. A dependency callee ends its branch. An
// in-module callee whose declaration the walk cannot read, or a call
// the type information cannot resolve, is a refusal serving fails
// closed on; the walk still finishes, and a hop found outranks the
// refusal as the reason. The walk answers serving's question (does the
// executed quantification draw from a run-time seed?), never the
// evidence class.
func (b *Backend) seededThrough(sel string, rootFn *types.Func, fd *ast.FuncDecl, pkg *packages.Package) (via, refusal string) {
	b.walkMu.Lock()
	defer b.walkMu.Unlock()
	b.initWalk()
	type frame struct {
		fn  *types.Func
		via string
	}
	seen := map[string]bool{}
	var queue []frame
	// The root's own callees come from the per-function memo where
	// the root is a declared function (every witness is); a body with
	// no object is resolved once here.
	var root walkedBody
	if rootFn != nil {
		b.bodyDrivesRunner(sel, rootFn, fd, pkg)
		root = b.bodyCallees[walkKey(sel, rootFn)]
	} else {
		root = staticCallees(fd.Body, pkg)
	}
	if len(root.unresolved) > 0 {
		refusal = seededRefusal(fmt.Errorf("call of %s in the bound body resolves to no declaration", root.unresolved[0]))
	}
	for _, fn := range root.callees {
		if k := declKey(fn); !seen[k] {
			seen[k] = true
			queue = append(queue, frame{fn: fn, via: fn.FullName()})
		}
	}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		fd, fpkg, err := b.funcDeclOf(sel, f.fn)
		if err != nil {
			if refusal == "" {
				refusal = seededRefusal(err)
			}
			continue
		}
		if fd == nil || fd.Body == nil {
			continue
		}
		if b.bodyDrivesRunner(sel, f.fn, fd, fpkg) {
			return f.via, ""
		}
		walked := b.bodyCallees[walkKey(sel, f.fn)]
		if len(walked.unresolved) > 0 && refusal == "" {
			refusal = seededRefusal(fmt.Errorf("call of %s in %s resolves to no declaration", walked.unresolved[0], f.fn.FullName()))
		}
		for _, callee := range walked.callees {
			if k := declKey(callee); !seen[k] {
				seen[k] = true
				queue = append(queue, frame{fn: callee, via: f.via})
			}
		}
	}
	return "", refusal
}

func (b *Backend) classifyWitness(symbol string) witnessVerdict {
	// Proof outranks property, property outranks example: resolved from
	// the body's callees. Only a test the witness run executes can
	// classify above example — a structural or rapid invocation in a
	// plain function never runs.
	if fd, pkg, err := b.funcDecl(symbol); err == nil && fd.Body != nil && runnableWitness(fd, pkg) {
		proof, property, rapidRef, structuralRef, gopterRef, dotImported := false, false, false, false, false, false
		// Every node is visited even after an assertion is seen: a
		// proof body that also drives a runner records both facts, so
		// the proof classification carries its seeding.
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				// A dot-imported use resolves the bare ident to the
				// library: named as its own near-miss, since the
				// classifying call must be a qualified selector.
				if obj := pkg.TypesInfo.Uses[ident]; obj != nil && obj.Pkg() != nil {
					if p := obj.Pkg().Path(); p == rapidPkg || p == structuralPkg || p == gopterPkg {
						dotImported = true
					}
				}
			}
			if sel, ok := n.(*ast.SelectorExpr); ok {
				// A reference without the classifying call is the
				// diagnosable near-miss: record which library the body
				// touches.
				if obj := pkg.TypesInfo.Uses[sel.Sel]; obj != nil && obj.Pkg() != nil {
					switch obj.Pkg().Path() {
					case rapidPkg:
						rapidRef = true
					case structuralPkg:
						structuralRef = true
					case gopterPkg:
						gopterRef = true
					}
				}
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := callTarget(call).(*ast.SelectorExpr); ok {
				if obj := pkg.TypesInfo.Uses[sel.Sel]; obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == structuralPkg && structuralAssertion(obj.Name()) {
					proof = true
				}
			}
			// The one gopter check driver counts; Property and generator
			// calls register without running.
			if driverCall(pkg, call) {
				property = true
			}
			return true
		})
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
			direct, via, refusal = b.seededInAnyView(symbol, b.selectionOf(pkg), fd, pkg)
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
		if b.fuzzTargetClass(symbol) == verify.PropertyWitness {
			return witnessVerdict{class: verify.PropertyWitness, inspected: true, seeded: direct, seededVia: via, seedingRefusal: refusal}
		}
		// An example verdict carries the transitive seeding class too:
		// a driver reached only through in-module helpers classifies
		// example on the evidence ladder and refuses serving, its
		// near-miss reason naming the hop the walk found.
		example := func(reason string) witnessVerdict {
			if via != "" {
				reason += " (reached through " + via + ")"
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
		case structuralRef:
			return example("no structural assertion invoked in the bound body")
		case dotImported:
			return example("recognized library reached through a dot import - only a qualified call classifies")
		default:
			return example("no property driver or analyzer call in the bound body")
		}
	}
	if b.fuzzTargetClass(symbol) == verify.PropertyWitness {
		return witnessVerdict{class: verify.PropertyWitness, inspected: true}
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

// seededInAnyView answers serving's one question for the symbol across
// every loaded view: the resolved view's body is walked (its direct
// driver call was classified already); every other view declaring the
// symbol has its own body tested for a direct driver call first —
// direct there, the symbol is random-seeded outright — then walked.
// A hop in any view seeds; a refusal in any view, a declaration of
// another view that cannot be read included, refuses.
func (b *Backend) seededInAnyView(symbol string, sel string, fd *ast.FuncDecl, pkg *packages.Package) (direct bool, via, refusal string) {
	root, _ := pkg.TypesInfo.Defs[fd.Name].(*types.Func)
	via, refusal = b.seededThrough(sel, root, fd, pkg)
	if via != "" {
		return false, via, ""
	}
	if b.singleView(symbol) {
		// One view holds the symbol: the resolved walk is the union.
		return false, "", refusal
	}
	others, otherRefusal := b.declsInOtherViews(symbol, sel)
	if refusal == "" {
		refusal = otherRefusal
	}
	for _, other := range others {
		b.walkMu.Lock()
		drives := b.bodyDrivesRunner(other.sel, other.fn, other.fd, other.pkg)
		b.walkMu.Unlock()
		if drives {
			return true, "", ""
		}
		v, r := b.seededThrough(other.sel, other.fn, other.fd, other.pkg)
		if v != "" {
			return false, v, ""
		}
		if refusal == "" {
			refusal = r
		}
	}
	return false, "", refusal
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
					refusal = seededRefusal(fmt.Errorf("%s in the %q view: %w", symbol, sel, err))
				}
			case fd != nil && fd.Body != nil:
				out = append(out, declaredFunc{fd: fd, pkg: fpkg, sel: sel, fn: fn})
			}
		}
	}
	return out, refusal
}

func (b *Backend) fuzzTargetClass(symbol string) verify.WitnessClass {
	pkgPath, rest := b.splitSymbol(symbol)
	if pkgPath == "" {
		return verify.ExampleWitness
	}
	parts := strings.Split(rest, ".")
	for _, pkg := range b.pkgs {
		if pkg.PkgPath != pkgPath && pkg.PkgPath != pkgPath+"_test" {
			continue
		}
		obj := lookup(pkg.Types, parts)
		fn, ok := obj.(*types.Func)
		if !ok {
			continue
		}
		sig := fn.Type().(*types.Signature)
		if sig.Params().Len() == 1 {
			if named, ok := sig.Params().At(0).Type().(*types.Pointer); ok {
				if t, ok := named.Elem().(*types.Named); ok &&
					t.Obj().Pkg() != nil && t.Obj().Pkg().Path() == "testing" && t.Obj().Name() == "F" {
					return verify.PropertyWitness
				}
			}
		}
	}
	return verify.ExampleWitness
}

// runnableWitness reports whether the declaration is a test the ordinary
// witness run executes: a Test or Fuzz function in a _test.go file taking
// the matching testing handle, per go test's naming rule (the name after
// the prefix must not start lowercase). Anything else never runs, so it
// can never produce evidence.
func runnableWitness(fd *ast.FuncDecl, pkg *packages.Package) bool {
	name := fd.Name.Name
	var prefix, handle string
	switch {
	case strings.HasPrefix(name, "Test"):
		prefix, handle = "Test", "T"
	case strings.HasPrefix(name, "Fuzz"):
		prefix, handle = "Fuzz", "F"
	default:
		return false
	}
	if rest := name[len(prefix):]; rest != "" {
		r, _ := utf8.DecodeRuneInString(rest)
		if unicode.IsLower(r) {
			return false
		}
	}
	if !strings.HasSuffix(pkg.Fset.Position(fd.Pos()).Filename, "_test.go") {
		return false
	}
	fn, ok := pkg.TypesInfo.Defs[fd.Name].(*types.Func)
	if !ok {
		return false
	}
	sig := fn.Type().(*types.Signature)
	if sig.Recv() != nil || sig.Params().Len() != 1 {
		return false
	}
	ptr, ok := sig.Params().At(0).Type().(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Pkg() != nil && obj.Pkg().Path() == "testing" && obj.Name() == handle
}

// shapeHash hashes the object's declared type rendered with fully
// qualified package paths.
func shapeHash(obj types.Object) string {
	return canon.Hash(types.ObjectString(obj, func(p *types.Package) string {
		return p.Path()
	}))
}

// generatedIn reports whether the object's declaration lies in a
// generated file, per the standard "Code generated ... DO NOT EDIT."
// marker. The object's declaring package is scanned — not the
// resolution candidate — so a method promoted from an embedded
// generated type is still detected, and a declaring package outside
// the load set (a dependency held as export data) is judged from its
// declaring file's own header.
// The judgment stays within the resolving package's own load: a
// promoted method's declaring file lives in the object's origin
// package - possibly a sibling of the resolving package - so the
// scan spans the load's packages sharing the origin path.
// Position containment is only meaningful inside one load's FileSet:
// with one package view per build selection, the same package path
// recurs across views with incompatible FileSets, and a cross-load
// scan would take the verdict from a foreign view's file
// nondeterministically (REQ-go-build-selections).
func (b *Backend) generatedIn(resolving *packages.Package, obj types.Object) bool {
	pos := obj.Pos()
	if !pos.IsValid() || obj.Pkg() == nil {
		return false
	}
	load := b.load[resolving]
	for _, pkg := range b.pkgs {
		if b.load[pkg] != load || pkg.PkgPath != obj.Pkg().Path() {
			continue
		}
		for _, f := range pkg.Syntax {
			if f.FileStart <= pos && pos < f.FileEnd {
				return ast.IsGenerated(f)
			}
		}
	}
	// The declaring package is outside this load — a scoped load's
	// dependency, held as export data — so the marker is read from the
	// declaring file's own header, the same judgment ast.IsGenerated
	// makes over a loaded file.
	if name := resolving.Fset.Position(pos).Filename; name != "" {
		return b.generatedHeader(name)
	}
	return false
}

// generatedHeader reports whether the file at path carries the
// standard "Code generated ... DO NOT EDIT." marker, parsing only its
// package clause and the comments before it, once per file.
func (b *Backend) generatedHeader(path string) bool {
	if verdict, ok := b.generatedHeaders.Load(path); ok {
		return verdict.(bool)
	}
	verdict := false
	if f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly|parser.ParseComments); err == nil {
		verdict = ast.IsGenerated(f)
	}
	b.generatedHeaders.Store(path, verdict)
	return verdict
}
