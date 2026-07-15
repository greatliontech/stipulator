package golang

import (
	"fmt"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/packages"

	"github.com/greatliontech/stipulator/internal/verify"
)

// Vacuous reports whether a test function contains no failure path: no
// failing testing call, no delegation to a callee receiving a testing
// handle, and no panic.
func (b *Backend) Vacuous(symbol string) (bool, error) {
	fd, pkg, err := b.funcDecl(symbol)
	if err != nil {
		return false, err
	}
	if fd.Body == nil {
		return true, nil
	}
	failing := map[string]bool{"Error": true, "Errorf": true, "Fatal": true, "Fatalf": true, "Fail": true, "FailNow": true}
	vacuous := true
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !vacuous {
			return vacuous
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" {
			vacuous = false
			return false
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && failing[sel.Sel.Name] {
			vacuous = false
			return false
		}
		for _, arg := range call.Args {
			if carriesTestingHandle(pkg.TypesInfo.TypeOf(arg)) {
				vacuous = false
				return false
			}
		}
		return true
	})
	return vacuous, nil
}

// carriesTestingHandle reports whether t is a testing handle (*testing.T,
// *testing.F, testing.TB) or a function type receiving one.
func carriesTestingHandle(t types.Type) bool {
	switch v := t.(type) {
	case nil:
		return false
	case *types.Pointer:
		if n, ok := v.Elem().(*types.Named); ok {
			return isTestingType(n)
		}
	case *types.Named:
		return isTestingType(v)
	case *types.Signature:
		for i := range v.Params().Len() {
			if carriesTestingHandle(v.Params().At(i).Type()) {
				return true
			}
		}
	}
	return false
}

func isTestingType(n *types.Named) bool {
	obj := n.Obj()
	return obj.Pkg() != nil && obj.Pkg().Path() == "testing" &&
		(obj.Name() == "T" || obj.Name() == "F" || obj.Name() == "B" || obj.Name() == "TB")
}

// funcDecl resolves a symbol to its declaring FuncDecl and package.
func (b *Backend) funcDecl(symbol string) (*ast.FuncDecl, *packages.Package, error) {
	res, _, err := b.Resolve(symbol)
	if err != nil {
		return nil, nil, err
	}
	if res != verify.Resolved {
		return nil, nil, fmt.Errorf("symbol %s does not resolve", symbol)
	}
	obj := b.object(symbol)
	if obj == nil {
		return nil, nil, fmt.Errorf("symbol %s has no object", symbol)
	}
	for _, pkg := range b.pkgs {
		for _, f := range pkg.Syntax {
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				if pkg.TypesInfo.Defs[fd.Name] == obj {
					return fd, pkg, nil
				}
			}
		}
	}
	return nil, nil, fmt.Errorf("symbol %s is not a function or method", symbol)
}
