package golang

import (
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

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

	// One row per fact: with one package view per build selection, the
	// same declaration is reached through two views' object identities,
	// and identity-keyed dedup would emit it twice. The key spans the
	// shape hash - same-named facts that genuinely differ (two types'
	// same-named methods, complementary tag variants) keep their rows -
	// and the frontier's add order is deterministic, so the emitted set
	// is canonical (REQ-go-slice, REQ-go-build-selections).
	emitted := map[[3]string]bool{}
	decls := make([]verify.Decl, 0, len(frontier))
	for _, obj := range frontier {
		pkgPath := ""
		if obj.Pkg() != nil {
			pkgPath = obj.Pkg().Path()
		}
		shape := shapeHash(obj)
		key := [3]string{pkgPath, obj.Name(), shape}
		if emitted[key] {
			continue
		}
		emitted[key] = true
		decls = append(decls, verify.Decl{
			Package:     pkgPath,
			Name:        obj.Name(),
			Declaration: types.ObjectString(obj, func(p *types.Package) string { return p.Path() }),
			ShapeHash:   shape,
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

// variantBase folds a test-variant build (the in-package
// "pkg [pkg.test]" or the external "pkg_test [pkg.test]") into the
// package under test, identified by build identity - never by path
// spelling: a real package whose import path ends in "_test" keeps its
// own path even when a sibling base package exists.
func variantBase(pkg *packages.Package) string {
	path := pkg.PkgPath
	if path == "" {
		path = pkg.ID
	}
	if strings.Contains(pkg.ID, " [") {
		// The ID fallback can carry the bracketed build identity -
		// strip it before the suffix trim. The current loader
		// populates PkgPath even on rebuilt non-root variant stubs,
		// so this arm guards drivers that surface only the ID,
		// keeping the fallback sound.
		if j := strings.Index(path, " ["); j >= 0 {
			path = path[:j]
		}
		return strings.TrimSuffix(path, "_test")
	}
	return path
}

// SliceFloor implements verify.FloorSlicer: the symbols' packages'
// transitive in-module import closure, each package carrying its
// disposition. Imports are the sound package-level over-approximation
// of every dependency channel the typed frontier misses - a reflection
// target must be linked, init effects and blank imports ride the
// import edge, and build-tag file selection is already resolved in the
// loaded view - so the floor over-approximates and never reads
// false-complete (REQ-go-slice). A first-hop dependency outside the
// loaded module is an explicit external row: the boundary is honest,
// never a silent cut.
func (b *Backend) SliceFloor(symbols []string, declaredPkgs []string) ([]verify.FloorPackage, error) {
	local := map[string]bool{}
	byPath := map[string][]*packages.Package{}
	for _, pkg := range b.pkgs {
		p := variantBase(pkg)
		local[p] = true
		byPath[p] = append(byPath[p], pkg)
	}
	declared := map[string]bool{}
	for _, p := range declaredPkgs {
		declared[p] = true
	}
	seeds := map[string]bool{}
	for _, sym := range symbols {
		if pkg := b.SymbolPackage(sym); pkg != "" {
			seeds[pkg] = true
		}
	}
	floor := map[string]bool{}
	external := map[string]bool{}
	var walk func(path string)
	walk = func(path string) {
		if floor[path] {
			return
		}
		floor[path] = true
		for _, pkg := range byPath[path] {
			for _, imp := range pkg.Imports {
				target := variantBase(imp)
				if local[target] {
					walk(target)
				} else if i := strings.IndexByte(target, '/'); (i > 0 && strings.Contains(target[:i], ".")) || (i < 0 && strings.Contains(target, ".")) {
					// Module dependencies only: the standard library
					// (no dot in the first path element) is excluded
					// exactly as the closure model excludes it - stable
					// under the pinned toolchain, not a frontier fact.
					external[target] = true
				}
			}
		}
	}
	for s := range seeds {
		if local[s] {
			walk(s)
		}
	}
	var out []verify.FloorPackage
	for p := range floor {
		disposition := "widened"
		if declared[p] {
			disposition = "declared"
		}
		out = append(out, verify.FloorPackage{Package: p, Disposition: disposition})
	}
	for p := range external {
		out = append(out, verify.FloorPackage{Package: p, Disposition: "external"})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Disposition != out[j].Disposition {
			return out[i].Disposition < out[j].Disposition
		}
		return out[i].Package < out[j].Package
	})
	return out, nil
}
