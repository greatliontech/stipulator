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
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/packages"

	"github.com/greatliontech/stipulator/internal/canon"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Backend resolves symbols within one Go tree: a single module, or a
// workspace whose go.work members are all in scope.
type Backend struct {
	pkgs []*packages.Package
	// dir is the absolute tree root New loaded, kept to reconcile
	// Fset-absolute file paths back to the tree-relative paths the corpus
	// and the git layer speak in.
	dir string
}

// New loads the tree rooted at dir, including test packages: the module
// alone, or every go.work member when the tree is a workspace — package
// patterns are module-scoped, so nested published modules would otherwise
// vanish from symbol resolution. A load failure is an error: per the
// spec, an unloadable tree is a verification error, never an absence.
func New(dir string) (*Backend, error) {
	members, err := workspaceMembers(dir)
	if err != nil {
		return nil, err
	}
	env := goworkEnv(dir)
	var pkgs []*packages.Package
	for _, m := range members {
		cfg := &packages.Config{
			Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
				packages.NeedTypes | packages.NeedTypesInfo,
			Dir:   filepath.Join(dir, m),
			Env:   env,
			Tests: true,
		}
		loaded, err := packages.Load(cfg, "./...")
		if err != nil {
			return nil, fmt.Errorf("loading Go packages in %s: %w", m, err)
		}
		pkgs = append(pkgs, loaded...)
	}
	// Deterministic candidate order regardless of load order.
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].ID < pkgs[j].ID })
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving tree root %s: %w", dir, err)
	}
	return &Backend{pkgs: pkgs, dir: abs}, nil
}

// FileSurface is the Go content of one changed source file, tree-relative:
// whether it is Go at all, whether it is generated, and the package-level
// function and method symbols it declares. It is the per-file input to the
// staged-delta hardening classification (REQ-harden-staged-scope).
type FileSurface struct {
	Path      string
	IsGo      bool
	Generated bool
	Symbols   []string
}

// Surface classifies the given tree-relative, slash-separated paths by Go
// content, reporting only the symbols whose body actually changed since HEAD:
// head supplies a path's HEAD bytes (ok=false when the path is new, so every
// symbol reads as changed). A symbol's body hash is compared against the same
// hash of the HEAD declaration of the same name — an unchanged body is
// dropped, so a one-function edit in a thirty-function file surfaces one
// symbol, not thirty. A path the loaded packages do not cover is still
// reported: IsGo is decided by extension, so a new-but-unloadable `.go` file
// reads as Go with no declared symbols (an unbound surface) rather than
// vanishing. Symbols are resolver symbol strings, sorted.
func (b *Backend) Surface(paths []string, head func(path string) ([]byte, bool)) []FileSurface {
	// Working-side declarations per tree-relative path: each declaration's
	// full resolver symbol and its body hash, keyed by short name so the
	// HEAD comparison matches within the file.
	type decl struct {
		symbol string
		hash   string
	}
	type fileDecls struct {
		generated bool
		byKey     map[string]decl
	}
	byPath := map[string]*fileDecls{}
	for _, pkg := range b.pkgs {
		pkgPath := strings.TrimSuffix(pkg.PkgPath, "_test")
		for _, f := range pkg.Syntax {
			abs := pkg.Fset.Position(f.Pos()).Filename
			rel, err := filepath.Rel(b.dir, abs)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			rel = filepath.ToSlash(rel)
			// Load(Tests: true) yields each file in both its normal and
			// its test-variant package; the AST is identical, so populate
			// once per path on first sighting to avoid double-counting.
			if _, seen := byPath[rel]; seen {
				continue
			}
			fdecls := &fileDecls{generated: ast.IsGenerated(f), byKey: map[string]decl{}}
			byPath[rel] = fdecls
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				sym := declSymbol(pkgPath, fn)
				if sym == "" {
					continue
				}
				src, err := b.sourceOf(pkg, bodyNode(fn))
				if err != nil {
					continue
				}
				fdecls.byKey[declKey(fn)] = decl{symbol: sym, hash: canon.Hash(canon.Text(string(src)))}
			}
		}
	}
	out := make([]FileSurface, 0, len(paths))
	for _, p := range paths {
		// Test files are witnesses, never mutation targets — out of scope
		// for a mutation-surface classification (REQ-harden-staged-scope).
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		fs := FileSurface{Path: p, IsGo: strings.HasSuffix(p, ".go")}
		if d := byPath[p]; d != nil {
			fs.Generated = d.generated
			var old map[string]string
			if hb, ok := head(p); ok {
				old = headDeclHashes(hb)
			}
			for key, wd := range d.byKey {
				if old != nil && old[key] == wd.hash {
					continue // body unchanged since HEAD
				}
				fs.Symbols = append(fs.Symbols, wd.symbol)
			}
			sort.Strings(fs.Symbols)
		}
		out = append(out, fs)
	}
	return out
}

// headDeclHashes parses HEAD file bytes and returns each top-level function
// or method's body hash, keyed by short name — the reference the working
// surface is diffed against. An unparseable prior version yields nil, so
// every working symbol reads as changed (conservative).
func headDeclHashes(src []byte) map[string]string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		node := bodyNode(fn)
		start := fset.Position(node.Pos()).Offset
		end := fset.Position(node.End()).Offset
		if start < 0 || end > len(src) || start > end {
			continue
		}
		out[declKey(fn)] = canon.Hash(canon.Text(string(src[start:end])))
	}
	return out
}

// bodyNode is the declaration's body when it has one, else the whole
// declaration — mirroring BodyHash, so the hashed span is behavior-bearing.
func bodyNode(fd *ast.FuncDecl) ast.Node {
	if fd.Body != nil {
		return fd.Body
	}
	return fd
}

// declKey is a declaration's within-file identity for HEAD comparison:
// "<Name>" for a function, "<Receiver>.<Name>" for a method.
func declKey(fd *ast.FuncDecl) string {
	if recv := recvTypeName(fd); recv != "" {
		return recv + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// declSymbol builds the resolver symbol string for a top-level declaration:
// "<pkg>.<Name>" for a function, "<pkg>.<Receiver>.<Name>" for a method.
func declSymbol(pkgPath string, fd *ast.FuncDecl) string {
	if recv := recvTypeName(fd); recv != "" {
		return pkgPath + "." + recv + "." + fd.Name.Name
	}
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		return "" // a receiver we cannot name — skip
	}
	return pkgPath + "." + fd.Name.Name
}

// recvTypeName is a method's receiver type name with the leading pointer star
// and any generic parameters stripped — "" for a plain function or an
// unnameable receiver.
func recvTypeName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	t := fd.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if idx, ok := t.(*ast.IndexExpr); ok { // Recv[T]
		t = idx.X
	}
	if idx, ok := t.(*ast.IndexListExpr); ok { // Recv[T, U]
		t = idx.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// Resolve implements verify.Backend.
func (b *Backend) Resolve(symbol string) (verify.Resolution, string, error) {
	pkgPath, rest := b.splitSymbol(symbol)
	if pkgPath == "" {
		return verify.NotFound, "", nil
	}
	parts := strings.Split(rest, ".")
	if len(parts) == 0 || len(parts) > 2 {
		return verify.NotFound, "", nil
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
		if b.generated(obj) {
			return verify.GeneratedFile, "", nil
		}
		return verify.Resolved, shapeHash(obj), nil
	}
	return verify.NotFound, "", nil
}

// splitSymbol finds the loaded package whose path prefixes the symbol
// (longest match wins) and returns it with the remainder.
func (b *Backend) splitSymbol(symbol string) (string, string) {
	best := ""
	for _, pkg := range b.pkgs {
		p := strings.TrimSuffix(pkg.PkgPath, "_test")
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
	case "NoImport", "Implements", "ExportedData", "FunctionSignature":
		return true
	default:
		return false
	}
}

// rapidPkg is the recognized property-test library: a test driving its
// check runner quantifies over generated inputs. Generator construction
// alone does not quantify, so only the drivers classify.
const rapidPkg = "pgregory.net/rapid"

func rapidDriver(name string) bool { return name == "Check" || name == "MakeCheck" }

// WitnessClass implements verify.WitnessClassifier: a test invoking the
// structural library yields an analyzer proof; a fuzz target — a function
// taking *testing.F — or a test driving a rapid check runner (a qualified
// or aliased rapid.Check / rapid.MakeCheck selector call in its own body)
// yields a property witness; everything else — including dot-imported
// driver calls — is an example witness. Resolved from the code, never
// declared.
func (b *Backend) WitnessClass(symbol string) verify.WitnessClass {
	// Proof outranks property, property outranks example: resolved from
	// the body's callees. Only a test the witness run executes can
	// classify above example — a structural or rapid invocation in a
	// plain function never runs.
	if fd, pkg, err := b.funcDecl(symbol); err == nil && fd.Body != nil && runnableWitness(fd, pkg) {
		proof, property := false, false
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || proof {
				return !proof
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
					}
				}
			}
			return true
		})
		switch {
		case proof:
			return verify.AnalyzerProof
		case property:
			return verify.PropertyWitness
		}
	}
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
func (b *Backend) generated(obj types.Object) bool {
	pos := obj.Pos()
	if !pos.IsValid() || obj.Pkg() == nil {
		return false
	}
	for _, pkg := range b.pkgs {
		if pkg.PkgPath != obj.Pkg().Path() {
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
