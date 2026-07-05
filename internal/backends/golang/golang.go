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
