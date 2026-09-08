package golang

import (
	"fmt"
	"go/ast"
	"go/types"

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
	if fn, ok := obj.(*types.Func); ok {
		// The object came from the first declaring view; its
		// declaration is read from that same view, never another's.
		if owner := b.typesPkg[fn.Pkg()]; owner != nil {
			b.walkMu.Lock()
			fd, pkg, err := b.funcDeclOf(b.selectionOf(owner), fn)
			b.walkMu.Unlock()
			if err != nil {
				return nil, nil, err
			}
			if fd != nil {
				return fd, pkg, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("symbol %s is not a function or method", symbol)
}
