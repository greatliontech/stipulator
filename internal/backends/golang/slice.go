package golang

import (
	"go/types"
	"sort"
	"strings"

	"github.com/greatliontech/stipulator/internal/verify"
)

// Slice implements verify.Slicer: the declarations of the transitive
// dependency frontier of the given symbols — the objects themselves plus
// every named type reachable from their signatures that is declared within
// the loaded module. Facts only: full declarations via the object printer,
// shape-pinned, canonically ordered by (package, name).
func (b *Backend) Slice(symbols []string) ([]verify.Decl, error) {
	local := map[string]bool{}
	for _, pkg := range b.pkgs {
		local[pkg.PkgPath] = true
	}

	seen := map[types.Object]bool{}
	var frontier []types.Object
	add := func(obj types.Object) {
		if obj == nil || seen[obj] {
			return
		}
		seen[obj] = true
		frontier = append(frontier, obj)
	}

	var walkType func(t types.Type, depth int)
	visited := map[types.Type]bool{}
	walkType = func(t types.Type, depth int) {
		if t == nil || visited[t] {
			return
		}
		visited[t] = true
		switch v := t.(type) {
		case *types.Named:
			tn := v.Obj()
			if tn != nil && tn.Pkg() != nil && local[tn.Pkg().Path()] {
				add(tn)
			}
			walkType(v.Underlying(), depth+1)
			for i := 0; i < v.TypeArgs().Len(); i++ {
				walkType(v.TypeArgs().At(i), depth+1)
			}
		case *types.Pointer:
			walkType(v.Elem(), depth)
		case *types.Slice:
			walkType(v.Elem(), depth)
		case *types.Array:
			walkType(v.Elem(), depth)
		case *types.Chan:
			walkType(v.Elem(), depth)
		case *types.Map:
			walkType(v.Key(), depth)
			walkType(v.Elem(), depth)
		case *types.Struct:
			for i := 0; i < v.NumFields(); i++ {
				walkType(v.Field(i).Type(), depth)
			}
		case *types.Signature:
			for i := 0; i < v.Params().Len(); i++ {
				walkType(v.Params().At(i).Type(), depth)
			}
			for i := 0; i < v.Results().Len(); i++ {
				walkType(v.Results().At(i).Type(), depth)
			}
		case *types.Interface:
			for i := 0; i < v.NumMethods(); i++ {
				walkType(v.Method(i).Type(), depth)
			}
		}
	}

	for _, sym := range symbols {
		res, _, err := b.Resolve(sym)
		if err != nil {
			return nil, err
		}
		if res != verify.Resolved {
			continue // absent or generated: nothing to slice
		}
		obj := b.object(sym)
		if obj == nil {
			continue
		}
		add(obj)
		walkType(obj.Type(), 0)
	}

	decls := make([]verify.Decl, 0, len(seen))
	for obj := range seen {
		pkgPath := ""
		if obj.Pkg() != nil {
			pkgPath = obj.Pkg().Path()
		}
		decls = append(decls, verify.Decl{
			Package:     pkgPath,
			Name:        obj.Name(),
			Declaration: types.ObjectString(obj, func(p *types.Package) string { return p.Path() }),
			ShapeHash:   shapeHash(obj),
		})
	}
	sort.Slice(decls, func(i, j int) bool {
		if decls[i].Package != decls[j].Package {
			return decls[i].Package < decls[j].Package
		}
		return decls[i].Name < decls[j].Name
	})
	return decls, nil
}

// object resolves a symbol to its types.Object (Resolve already validated
// existence).
func (b *Backend) object(symbol string) types.Object {
	pkgPath, rest := b.splitSymbol(symbol)
	if pkgPath == "" {
		return nil
	}
	parts := strings.Split(rest, ".")
	for _, pkg := range b.pkgs {
		if pkg.PkgPath != pkgPath && pkg.PkgPath != pkgPath+"_test" {
			continue
		}
		if obj := lookup(pkg.Types, parts); obj != nil {
			return obj
		}
	}
	return nil
}
