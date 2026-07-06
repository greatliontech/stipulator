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
	"go/types"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/packages"

	"github.com/greatliontech/stipulator/internal/canon"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Backend resolves symbols within one Go module tree.
type Backend struct {
	pkgs []*packages.Package
}

// New loads the module rooted at dir, including test packages. A load
// failure is an error: per the spec, an unloadable tree is a verification
// error, never an absence.
func New(dir string) (*Backend, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo,
		Dir:   dir,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("loading Go packages: %w", err)
	}
	// Deterministic candidate order regardless of load order.
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].ID < pkgs[j].ID })
	return &Backend{pkgs: pkgs}, nil
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
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if obj := pkg.TypesInfo.Uses[sel.Sel]; obj != nil && obj.Pkg() != nil {
					switch {
					case obj.Pkg().Path() == structuralPkg:
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
