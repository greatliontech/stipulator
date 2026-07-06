// Package structural provides analyzer assertions for structural spec
// clauses: authored as ordinary Go tests, executed in the witness run, and
// recognized by the stipulator Go backend as the proof evidence class —
// resolved from the invoking code, never declared. Parameters live in the
// test source, type-checked and reviewable.
package structural

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// NoImport asserts that no package matched by fromPattern imports any of
// the forbidden packages, transitively. A forbidden entry matches exactly,
// or as a subtree with a trailing "/...". Failures name the shortest
// import chain. The assertion reads the production import graph: test
// files of the matched packages are not constrained.
func NoImport(tb testing.TB, fromPattern string, forbidden ...string) {
	tb.Helper()
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps,
	}, fromPattern)
	if err != nil {
		tb.Fatalf("structural.NoImport: loading %s: %v", fromPattern, err)
		return
	}
	if len(pkgs) == 0 {
		tb.Fatalf("structural.NoImport: %s matches no packages — the constraint is vacuous", fromPattern)
		return
	}
	matches := func(path string) bool {
		for _, f := range forbidden {
			if sub, ok := strings.CutSuffix(f, "/..."); ok {
				if path == sub || strings.HasPrefix(path, sub+"/") {
					return true
				}
			} else if path == f {
				return true
			}
		}
		return false
	}
	for _, root := range pkgs {
		if len(root.Errors) > 0 {
			tb.Fatalf("structural.NoImport: %s has load errors: %v", root.PkgPath, root.Errors[0])
			return
		}
		// BFS yields the shortest chain from the root to a forbidden
		// package.
		parent := map[string]string{}
		queue := []*packages.Package{root}
		seen := map[string]bool{root.PkgPath: true}
		for len(queue) > 0 {
			pkg := queue[0]
			queue = queue[1:]
			paths := make([]string, 0, len(pkg.Imports))
			for p := range pkg.Imports {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			for _, p := range paths {
				if seen[p] {
					continue
				}
				seen[p] = true
				parent[p] = pkg.PkgPath
				if matches(p) {
					chain := []string{p}
					for at := pkg.PkgPath; at != ""; at = parent[at] {
						chain = append([]string{at}, chain...)
					}
					tb.Errorf("structural.NoImport: %s imports %s\n  chain: %s",
						root.PkgPath, p, strings.Join(chain, " -> "))
					continue
				}
				queue = append(queue, pkg.Imports[p])
			}
		}
	}
}

// Implements asserts that a type satisfies an interface. Pass pointers so
// nil values carry the types: Implements(t, (*ast.Ident)(nil),
// (*ast.Node)(nil)). Failures name the missing or mismatched method.
func Implements(tb testing.TB, typ, iface any) {
	tb.Helper()
	it := reflect.TypeOf(iface)
	if it == nil || it.Kind() != reflect.Pointer || it.Elem().Kind() != reflect.Interface {
		tb.Fatalf("structural.Implements: iface must be a nil interface pointer like (*io.Reader)(nil), got %T", iface)
		return
	}
	ifaceType := it.Elem()
	tt := reflect.TypeOf(typ)
	if tt == nil {
		tb.Fatalf("structural.Implements: typ carries no type; pass a typed nil pointer")
		return
	}
	if tt.Implements(ifaceType) {
		return
	}
	for i := range ifaceType.NumMethod() {
		m := ifaceType.Method(i)
		if _, ok := tt.MethodByName(m.Name); !ok {
			tb.Errorf("structural.Implements: %s does not implement %s: missing method %s", tt, ifaceType, m.Name)
			return
		}
	}
	tb.Errorf("structural.Implements: %s does not implement %s: method signatures differ", tt, ifaceType)
}
