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
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/greatliontech/go-x-tools/go/packages"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/gotool"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/verify"
	"golang.org/x/mod/modfile"
)

// Backend resolves symbols within one Go tree: a single module, or a
// workspace whose go.work members are all in scope.
type Backend struct {
	pkgs []*packages.Package
	// exportFailures are observations from the same typed load as each
	// package, never a later view's attempt to explain an earlier failure.
	exportFailures map[*packages.Package]*missingExportError
	// sourceLoads holds on-demand declaring packages by their original
	// load configuration. Export data's positions may name //line aliases,
	// so only source can establish a promoted method's physical file.
	sourceMu    sync.Mutex
	sourceLoads map[sourceLoadKey]sourceLoad
	// viewErrors names declared views whose load failed whole (a broken
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
	// modules maps each member to its module path, read once.
	modules map[string]string
	// lazyCfg holds one load configuration per member and build
	// selection, captured for every member at construction — a member
	// owning no scoped pattern included — and consulted under the
	// walking witness's own selection, so a tag-split helper is read in
	// the view the witness runs in, and the seeding walk can load an
	// in-module callee's package the scoped load did not hold, so the
	// walk answers the same whatever the load's scope
	// (REQ-evidence-witness-freshness-seeded). Its context is the resolver's
	// own.
	lazyCfg map[string]map[string]*packages.Config
	// walkMu guards the seeding walk's memos, all keyed by build
	// selection and the declaration's stable name (declKey): declIndex
	// maps each loaded function to its declaration, extended as lazy
	// loads land; lazyLoaded records each on-demand package load per
	// selection with its error (a cancellation is never recorded);
	// notInModule memoizes the dependency verdict per package path;
	// bodies memoizes, per declared function, whether its own body
	// directly drives a runner (and where) and the static callees it
	// resolves at their call sites. The lock is held across an
	// on-demand go list: the walk is serialized, and a load must not
	// race the index it extends.
	walkMu      sync.Mutex
	declIndex   map[string]declaredFunc
	lazyLoaded  map[string]error
	notInModule map[string]bool
	bodies      map[string]bodyFacts
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
// callees the type information resolves, each at its call site, and
// the calls it could not, each at its own.
type walkedBody struct {
	callees    []calleeRef
	unresolved []calleeRef
}

// calleeRef is one call of a body: the declared origin the type
// information resolved — nil for an unresolved call, which is named
// instead — at the call's tree-relative site.
type calleeRef struct {
	fn   *types.Func
	name string
	site string
}

// bodyFacts is one declared body's memo under one selection: its direct
// evidence-tier signals (proof assertion, property driver call, and
// near-miss library references), whether it drives or names a
// run-time-seeded runner in any spelling, the driving call's or
// reference's site, and its walked callees — the one scan classifyWitness,
// serving's walk, and the explain derivation read.
type bodyFacts struct {
	proof         bool
	property      bool
	rapidRef      bool
	structuralRef bool
	gopterRef     bool
	quickRef      bool
	dotImported   bool
	drives        bool
	driverSite    string
	callees       walkedBody
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
// module, workspace members and accepted independent module roots —
// package patterns are module-scoped, so nested published modules would
// otherwise vanish from symbol resolution. A load failure is an error:
// per the spec, an unloadable tree is a verification error, never an
// absence. It loads every visible member under every build selection,
// over "./..." — or, when patterns are given, over exactly those packages
// (an explicitly empty slice loads none), the scope a served resolution's
// stale remainder needs (REQ-evidence-resolution-freshness-typed-load). Their
// dependencies stay export data; the one verdict that reads a
// dependency's source — a promoted method's declaring file, for the
// generated-file marker — loads that declaring package on demand under
// the same selection, so a scoped load answers as the whole-tree load does.
func newContext(ctx context.Context, dir string, patterns []string) (*Backend, error) {
	env, err := goworkEnv(dir)
	if err != nil {
		return nil, err
	}
	selections, crossPlatform, err := policyBuildSelections(dir)
	if err != nil {
		return nil, err
	}
	members, err := resolutionMembers(dir, selections)
	if err != nil {
		return nil, err
	}
	var pkgs []*packages.Package
	exportFailures := map[*packages.Package]*missingExportError{}
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
	// Each declared member's module path is read once; each view restricts
	// ownership to the members its workspace context actually includes.
	modules, err := memberModules(dir, members)
	if err != nil {
		return nil, fmt.Errorf("loading Go packages: %w", err)
	}
	lazyCfg := map[string]map[string]*packages.Config{}
	// One toolchain sampler for this load — every selection's members
	// sample through it.
	sample := newToolchainSample()
	for _, sel := range selections {
		viewEnv := selectionViewEnv(env, sel)
		viewMembers, err := sel.members(dir)
		if err != nil {
			return nil, err
		}
		viewModules := map[string]string{}
		for _, member := range viewMembers {
			viewModules[member] = modules[member]
		}
		owned := memberPatterns(viewModules, patterns)
		// The selection view is a frontend parse of the selection's own
		// sources, so it inherits the toolchain-provenance prerequisite,
		// sampled per member where each view loads: an identified
		// selection toolchain this binary's frontend cannot read refuses
		// the run (REQ-evidence-toolchain-provenance), while a member
		// whose toolchain cannot be sampled loads no view and falls
		// through to the per-view unloadable degradation below
		// (REQ-go-build-selections).
		if err := checkSelectionMembers(ctx, dir, viewEnv, viewMembers, sel.toolchain, sample); err != nil {
			return nil, err
		}
		var viewPkgs []*packages.Package
		viewLoads := map[*packages.Package]int{}
		viewFailed := false
		for _, m := range viewMembers {
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
			lazyCfg[m][sel.key()] = &lazy
			load := []string{"./..."}
			if patterns != nil {
				// A pattern loads from the one member whose module
				// owns it: loading every member over the same import
				// paths would hold each package once per member.
				load = owned[m]
				if len(load) == 0 {
					continue
				}
			}
			loaded, failures, err := loadTypedPackages(cfg, load...)
			if err == nil && patterns != nil {
				loaded = matchedRoots(loaded)
			}
			if err != nil {
				if sel.isDefault() {
					return nil, fmt.Errorf("loading Go packages in %s: %w", m, err)
				}
				// A declared view that cannot load degrades to a named
				// refusal instead of failing the whole binding context:
				// the pre-existing views' binding health is the floor,
				// and the view's own symbols refuse with this reason.
				viewErrors = append(viewErrors, fmt.Sprintf("build selection %s: loading Go packages in %s: %v", viewLabel(sel.key()), m, err))
				viewFailed = true
				break
			}
			for _, pkg := range loaded {
				viewLoads[pkg] = loads
				if failure := failures[pkg]; failure != nil {
					exportFailures[pkg] = failure
				}
			}
			loadSelection[loads] = sel.key()
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
		// in selection order - default first, then the policy's declared
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
	return &Backend{pkgs: pkgs, exportFailures: exportFailures, load: loadIndex, loadSelection: loadSelection, viewErrors: viewErrors, dir: abs, modules: modules, lazyCfg: lazyCfg, typesPkg: typesPkg, byPath: byPath}, nil
}

// matchedRoots drops the roots a scoped load's patterns matched nothing
// for: go list answers a vanished package's path with an error entry
// carrying no Go files, which "./..." never enumerates, so the scoped
// load must not hold it either — its symbols answer not found, as the
// whole-tree load answers, never a load error. A fileless read or tool
// failure remains a root: it cannot establish an absent package boundary.
func matchedRoots(loaded []*packages.Package) []*packages.Package {
	kept := loaded[:0]
	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 && len(pkg.GoFiles) == 0 {
			absent := true
			for _, err := range pkg.Errors {
				absent = absent && explicitPackageAbsence(err.Msg)
			}
			if absent {
				continue
			}
		}
		kept = append(kept, pkg)
	}
	return kept
}

// memberModules reads each workspace member's module path once — the
// ownership table memberPatterns and the seeding walk's in-module
// judgment both consult.
func memberModules(dir string, members []string) (map[string]string, error) {
	modules := map[string]string{}
	for _, m := range members {
		data, err := os.ReadFile(filepath.Join(dir, m, "go.mod"))
		if err != nil {
			return nil, fmt.Errorf("reading resolution module %q: %w", m, err)
		}
		if path := modfile.ModulePath(data); path != "" {
			modules[m] = path
		} else {
			return nil, fmt.Errorf("resolution module %q: go.mod declares no module path", m)
		}
	}
	return modules, nil
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
			if moduleOwns(path, pattern) && len(path) > longest {
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
// the selection's workspace pin and its own toolchain when it
// declares one — exactly as its invocation executes, since a
// toolchain'd tag view under the ambient toolchain would miss the
// selection's own stdlib surface (REQ-go-build-selections). The one
// derivation the backend's typed views and the served
// resolution's freshness views share, so a record's fingerprint is
// checked under the environment that produced it.
func selectionViewEnv(env []string, sel buildSelection) []string {
	env = selectionWorkspace(env, sel.workspaceMode)
	if sel.toolchain == "" {
		return env
	}
	return gotool.SetEnv(env, "GOTOOLCHAIN", sel.toolchain)
}

// selectionViewFlags is the build selection's package-load flags: its
// effective tag set and module mode. Race is a tag here: the views load
// sources, they never instrument.
func selectionViewFlags(sel buildSelection) []string {
	var flags []string
	if len(sel.tags) > 0 {
		flags = append(flags, "-tags="+strings.Join(sel.tags, ","))
	}
	if mode := moduleModeFlag(sel.moduleMode); mode != "" {
		flags = append(flags, mode)
	}
	return flags
}

// key is the record and lazy-load coordinate of the complete selection.
// Policy tags are sorted and deduplicated before constructing a selection.
func (s buildSelection) key() string {
	if s.isDefault() {
		return "default"
	}
	return strings.Join(s.tags, ",") + "\x00" + s.toolchain + "\x00" + moduleModeFlag(s.moduleMode) + "\x00" + s.moduleRoot + "\x00" + workspaceWord(s.workspaceMode)
}

func (s buildSelection) isDefault() bool {
	return len(s.tags) == 0 && s.toolchain == "" && s.moduleMode == stipulatorv1.GoModuleMode_GO_MODULE_MODE_UNSPECIFIED && s.moduleRoot == "" && s.workspaceMode == stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_UNSPECIFIED
}

// buildSelection is one resolution view's identity: the invocation's
// effective tag-set (declared tags plus the implicit `race` tag of a
// -race invocation), toolchain, module mode and module/workspace context
// (REQ-go-build-selections).
type buildSelection struct {
	tags          []string
	toolchain     string
	moduleMode    stipulatorv1.GoModuleMode
	moduleRoot    string
	workspaceMode stipulatorv1.GoWorkspaceMode
}

// policyBuildSelections derives the resolution views from the accepted
// policy record: the default contexts first, then one view per
// distinct invocation selection in the policy's canonical invocation
// order (REQ-go-build-selections). The policy is the authority on
// which build selections exist - execution discovery already runs
// them - so resolution reads the same record rather than growing a
// configuration surface. A tree without a record resolves the default
// view alone; a malformed record is a verification error, never a
// silent narrowing.
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
	seen := map[string]bool{selections[0].key(): true}
	var declared []buildSelection
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
		if err := validateModuleRoot(cfg.GetModuleRoot()); err != nil {
			return nil, nil, err
		}
		if cfg.GetModuleRoot() != "" {
			root, err := resolvedTreeRoot(dir)
			if err != nil {
				return nil, nil, err
			}
			if err := resolvedUnder(root, dir, filepath.FromSlash(cfg.GetModuleRoot())); err != nil {
				return nil, nil, fmt.Errorf("resolution module_root: %w", err)
			}
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(cfg.GetModuleRoot()), "go.mod")); err != nil {
				return nil, nil, fmt.Errorf("resolution module_root %q: %w", cfg.GetModuleRoot(), err)
			}
		}
		switch cfg.GetWorkspaceMode() {
		case stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_UNSPECIFIED, stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF:
		case stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE:
			if _, err := os.Stat(filepath.Join(dir, "go.work")); err != nil {
				return nil, nil, fmt.Errorf("resolution workspace_mode WORKSPACE: %w", err)
			}
		default:
			return nil, nil, fmt.Errorf("resolution workspace_mode %d is unsupported", cfg.GetWorkspaceMode())
		}
		sel := buildSelection{tags: tags, toolchain: cfg.GetToolchain(), moduleMode: cfg.GetModuleMode(), moduleRoot: cfg.GetModuleRoot(), workspaceMode: cfg.GetWorkspaceMode()}
		if sel.moduleRoot != "" || sel.workspaceMode != stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_UNSPECIFIED {
			base := buildSelection{moduleRoot: sel.moduleRoot, workspaceMode: sel.workspaceMode}
			if !seen[base.key()] {
				seen[base.key()] = true
				selections = append(selections, base)
			}
		}
		declared = append(declared, sel)
	}
	for _, sel := range declared {
		key := sel.key()
		if seen[key] {
			continue
		}
		seen[key] = true
		selections = append(selections, sel)
	}
	return selections, crossPlatform, nil
}

// Resolve implements verify.Backend.
func (b *Backend) Resolve(symbol string) (verify.Resolution, string, error) {
	res, shape, _, err := b.ResolveIn(symbol)
	return res, shape, err
}

// ResolveIn is Resolve naming the build selection whose view resolved
// the symbol, empty when it did not resolve: the
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
			if failure := b.exportFailures[pkg]; failure != nil {
				return verify.NotFound, "", "", fmt.Errorf("package %s has load errors: %w", pkg.ID, failure)
			}
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
		source, err := b.declarationSource(pkg, obj)
		if err != nil {
			return verify.NotFound, "", "", err
		}
		if source.file != nil && ast.IsGenerated(source.file) {
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
		source, err := b.declarationSource(pkg, obj)
		if err != nil || source.name == "" {
			return "", false
		}
		rel, err := filepath.Rel(b.dir, source.name)
		if err != nil || !inTreeRel(rel) {
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

// lookup finds a package-scope object, or a method or struct field through its
// enclosing type name.
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
	if _, ok := tn.Type().Underlying().(*types.Struct); ok {
		if fieldObj, _, _ := types.LookupFieldOrMethod(tn.Type(), true, pkg, parts[1]); fieldObj != nil {
			if v, ok := fieldObj.(*types.Var); ok && v.IsField() {
				return v.Origin()
			}
		}
	}
	return nil
}
