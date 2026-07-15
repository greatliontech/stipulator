package golang

import (
	"fmt"
	"go/ast"

	"golang.org/x/tools/go/packages"

	"github.com/greatliontech/stipulator/internal/verify"
)

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
