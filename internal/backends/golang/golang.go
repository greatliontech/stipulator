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
	// dir is the absolute tree root New loaded, kept to reconcile
	// Fset-absolute file paths back to the tree-relative paths the corpus
	// and the git layer speak in.
	dir string
}

// newContext loads the tree rooted at dir, including test packages: the
// module alone, or every go.work member when the tree is a workspace —
// package patterns are module-scoped, so nested published modules would
// otherwise vanish from symbol resolution. A load failure is an error:
// per the spec, an unloadable tree is a verification error, never an
// absence. Deliberately unexported: in-process loading spawns go list
// outside any owned process group, so the only cross-package door to
// package discovery is the owned resolver client (NewOwned), keeping
// REQ-go-owned-processes structurally satisfied for every consumer.
func newContext(ctx context.Context, dir string) (*Backend, error) {
	members, err := workspaceMembers(dir)
	if err != nil {
		return nil, err
	}
	env := goworkEnv(dir)
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
	loads := 0
	for _, sel := range selections {
		viewEnv := env
		if sel.toolchain != "" {
			// The selection's view loads under the selection's
			// toolchain, exactly as its invocation executes - a
			// toolchain'd tag view under the ambient toolchain would
			// miss the selection's own stdlib surface
			// (REQ-go-build-selections).
			viewEnv = append(dropEnv(append([]string(nil), env...), "GOTOOLCHAIN"), "GOTOOLCHAIN="+sel.toolchain)
		}
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
			if len(sel.tags) > 0 {
				cfg.BuildFlags = []string{"-tags=" + strings.Join(sel.tags, ",")}
			}
			loaded, err := packages.Load(cfg, "./...")
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
	return &Backend{pkgs: pkgs, load: loadIndex, viewErrors: viewErrors, dir: abs}, nil
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
	// The degraded-view refusal precedes every silent-NotFound arm: a
	// reference the loaded views cannot even prefix-match could live in
	// the unloadable view, and the amended contract forbids a silent
	// absence that masks one (REQ-go-build-selections).
	notFound := func() (verify.Resolution, string, error) {
		if len(b.viewErrors) > 0 {
			return verify.NotFound, "", fmt.Errorf("symbol unresolved and %d build-selection view(s) failed to load: %s", len(b.viewErrors), strings.Join(b.viewErrors, "; "))
		}
		return verify.NotFound, "", nil
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
			return verify.NotFound, "", fmt.Errorf("package %s has load errors: %v", pkg.ID, pkg.Errors[0])
		}
		obj := lookup(pkg.Types, parts)
		if obj == nil {
			continue
		}
		if b.generatedIn(pkg, obj) {
			return verify.GeneratedFile, "", nil
		}
		return verify.Resolved, shapeHash(obj), nil
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
	// Proof outranks property, property outranks example: resolved from
	// the body's callees. Only a test the witness run executes can
	// classify above example — a structural or rapid invocation in a
	// plain function never runs.
	if fd, pkg, err := b.funcDecl(symbol); err == nil && fd.Body != nil && runnableWitness(fd, pkg) {
		proof, property, rapidRef, structuralRef, gopterRef, dotImported := false, false, false, false, false, false
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if proof {
				return false
			}
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
			// A generic instantiation wraps the selector in an index
			// expression: structural.Implements[io.Reader](t, x) is
			// still a direct invocation.
			fun := call.Fun
			switch idx := fun.(type) {
			case *ast.IndexExpr:
				fun = idx.X
			case *ast.IndexListExpr:
				fun = idx.X
			}
			if sel, ok := fun.(*ast.SelectorExpr); ok {
				if obj := pkg.TypesInfo.Uses[sel.Sel]; obj != nil && obj.Pkg() != nil {
					switch {
					case obj.Pkg().Path() == structuralPkg && structuralAssertion(obj.Name()):
						proof = true
						return false
					case obj.Pkg().Path() == rapidPkg && rapidDriver(sel.Sel.Name):
						property = true
					case obj.Pkg().Path() == gopterPkg && sel.Sel.Name == "TestingRun":
						// The one gopter check driver; Property and
						// generator calls register without running.
						property = true
					}
				}
			}
			return true
		})
		switch {
		case proof:
			return verify.AnalyzerProof, ""
		case property:
			return verify.PropertyWitness, ""
		}
		// A fuzz target quantifies by its harness whatever its body
		// calls - the signature check below classifies it property.
		if b.fuzzTargetClass(symbol) == verify.PropertyWitness {
			return verify.PropertyWitness, ""
		}
		switch {
		case rapidRef:
			return verify.ExampleWitness, "rapid.Check not invoked in the bound body"
		case gopterRef:
			return verify.ExampleWitness, "gopter.Properties.TestingRun not invoked in the bound body"
		case structuralRef:
			return verify.ExampleWitness, "no structural assertion invoked in the bound body"
		case dotImported:
			return verify.ExampleWitness, "recognized library reached through a dot import - only a qualified call classifies"
		default:
			return verify.ExampleWitness, "no property driver or analyzer call in the bound body"
		}
	}
	class := b.fuzzTargetClass(symbol)
	if class == verify.PropertyWitness {
		return class, ""
	}
	return verify.ExampleWitness, "not a runnable test witness"
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

// generated reports whether the object's declaration lies in a generated
// file, per the standard "Code generated ... DO NOT EDIT." marker. The
// object's declaring package is scanned — not the resolution candidate —
// so a method promoted from an embedded generated type is still detected.
// A declaring package outside the load set cannot be checked and reads as
// not generated.
// generatedIn judges the object within the resolving package's own
// load: a promoted method's declaring file lives in the object's
// origin package - possibly a sibling of the resolving package - so
// the scan spans the load's packages sharing the origin path.
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
	return false
}
