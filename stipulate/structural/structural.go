// Package structural provides analyzer assertions for structural spec
// clauses: authored as ordinary Go tests, executed in the witness run, and
// recognized by the stipulator Go backend as the proof evidence class —
// resolved from the invoking code, never declared. Parameters live in the
// test source, type-checked and reviewable.
package structural

import (
	"go/token"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// ImportRule declares the direct production imports one package may use.
// Internal and ThirdParty are always exact allowlists. StandardLibrary is
// exact when RestrictStandardLibrary is true and documentary otherwise.
type ImportRule struct {
	Internal                []string
	ThirdParty              []string
	StandardLibrary         []string
	RestrictStandardLibrary bool
}

// ImportAllowlist asserts that every production package matched by fromPattern
// has a policy row and that its direct imports obey that row. Same-module and
// third-party imports are always denied unless listed. Standard-library imports
// remain open unless a row sets RestrictStandardLibrary, which lets capability
// boundaries enumerate their complete stdlib surface.
func ImportAllowlist(tb testing.TB, fromPattern string, rules map[string]ImportRule) {
	tb.Helper()
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedModule,
	}, fromPattern)
	if err != nil {
		tb.Fatalf("structural.ImportAllowlist: loading %s: %v", fromPattern, err)
		return
	}
	if len(pkgs) == 0 {
		tb.Fatalf("structural.ImportAllowlist: %s matches no packages — the constraint is vacuous", fromPattern)
		return
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].PkgPath < pkgs[j].PkgPath })
	matched := make(map[string]bool, len(pkgs))
	modulePath := ""
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			tb.Fatalf("structural.ImportAllowlist: %s has load errors: %v", pkg.PkgPath, pkg.Errors[0])
			return
		}
		if len(pkg.GoFiles) == 0 {
			continue
		}
		if pkg.Module == nil || pkg.Module.Path == "" {
			tb.Fatalf("structural.ImportAllowlist: %s is not in a module", pkg.PkgPath)
			return
		}
		if modulePath == "" {
			modulePath = pkg.Module.Path
		} else if pkg.Module.Path != modulePath {
			tb.Fatalf("structural.ImportAllowlist: pattern spans modules %s and %s", modulePath, pkg.Module.Path)
			return
		}
		matched[pkg.PkgPath] = true
		rule, ok := rules[pkg.PkgPath]
		if !ok {
			tb.Errorf("structural.ImportAllowlist: production package %s has no policy row", pkg.PkgPath)
			continue
		}
		for _, importPath := range sortedImportPaths(pkg.Imports) {
			imported := pkg.Imports[importPath]
			switch {
			case imported.Module != nil && imported.Module.Path == modulePath:
				if !slices.Contains(rule.Internal, importPath) {
					tb.Errorf("structural.ImportAllowlist: %s imports unlisted internal package %s", pkg.PkgPath, importPath)
				}
			case imported.Module != nil:
				if !slices.Contains(rule.ThirdParty, importPath) {
					tb.Errorf("structural.ImportAllowlist: %s imports unlisted third-party package %s", pkg.PkgPath, importPath)
				}
			case rule.RestrictStandardLibrary && !slices.Contains(rule.StandardLibrary, importPath):
				tb.Errorf("structural.ImportAllowlist: %s imports unlisted standard-library package %s", pkg.PkgPath, importPath)
			}
		}
	}
	for pkg := range rules {
		if !matched[pkg] {
			tb.Errorf("structural.ImportAllowlist: policy row %s matches no production package", pkg)
		}
	}
}

func sortedImportPaths(imports map[string]*packages.Package) []string {
	paths := make([]string, 0, len(imports))
	for path := range imports {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// Field describes one exported field in an ExportedData assertion.
type Field struct {
	Name string
	Type reflect.Type
}

// FieldOf constructs an expected exported field without requiring callers to
// manipulate reflect.Type values directly.
func FieldOf[T any](name string) Field {
	return Field{Name: name, Type: reflect.TypeFor[T]()}
}

// ExportedData asserts that T resolves to an exported named struct whose complete
// field list exactly matches fields in declaration order. Every field must be
// exported, non-embedded, and untagged, and neither the value nor pointer type may
// expose exported methods. Exact names and types plus those exclusions make hidden
// state or type-owned serialization behavior a structural failure.
func ExportedData[T any](tb testing.TB, fields ...Field) {
	tb.Helper()
	typ := reflect.TypeFor[T]()
	if typ.Kind() != reflect.Struct {
		tb.Fatalf("structural.ExportedData: %s is %s, want struct", typ, typ.Kind())
		return
	}
	if typ.Name() == "" || typ.PkgPath() == "" || !token.IsExported(typ.Name()) {
		tb.Fatalf("structural.ExportedData: %s is not an exported named type", typ)
		return
	}
	if typ.NumField() != len(fields) {
		tb.Errorf("structural.ExportedData: %s has %d fields, want %d", typ, typ.NumField(), len(fields))
		return
	}
	if typ.NumMethod() != 0 || reflect.PointerTo(typ).NumMethod() != 0 {
		tb.Errorf("structural.ExportedData: %s has methods on its value or pointer type", typ)
		return
	}
	for i, want := range fields {
		got := typ.Field(i)
		if !got.IsExported() {
			tb.Errorf("structural.ExportedData: %s field %d %q is unexported", typ, i, got.Name)
			return
		}
		if got.Anonymous {
			tb.Errorf("structural.ExportedData: %s field %d %q is embedded", typ, i, got.Name)
			return
		}
		if got.Tag != "" {
			tb.Errorf("structural.ExportedData: %s field %d %q has tag %q", typ, i, got.Name, got.Tag)
			return
		}
		if want.Name == "" || want.Type == nil {
			tb.Fatalf("structural.ExportedData: expected field %d is incomplete: %+v", i, want)
			return
		}
		if got.Name != want.Name || got.Type != want.Type {
			tb.Errorf("structural.ExportedData: %s field %d = %s %s, want %s %s", typ, i, got.Name, got.Type, want.Name, want.Type)
			return
		}
	}
}

// FunctionSignature asserts that fn is a function whose complete signature is
// exactly Sig. Sig must itself be a function type.
func FunctionSignature[Sig any](tb testing.TB, fn any) {
	tb.Helper()
	want := reflect.TypeFor[Sig]()
	if want.Kind() != reflect.Func {
		tb.Fatalf("structural.FunctionSignature: signature type must be a function, got %s", want)
		return
	}
	got := reflect.TypeOf(fn)
	if got == nil {
		tb.Fatalf("structural.FunctionSignature: function value carries no type")
		return
	}
	if got.Kind() != reflect.Func {
		tb.Fatalf("structural.FunctionSignature: value has type %s, want function %s", got, want)
		return
	}
	if got != want {
		tb.Errorf("structural.FunctionSignature: got %s, want %s", got, want)
	}
}

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

// Implements asserts that a type satisfies the interface I. Pass a typed
// nil pointer so the value carries the type:
// Implements[ast.Node](t, (*ast.Ident)(nil)). Failures name the missing
// or mismatched method; instantiating with a non-interface I is refused —
// that would silently assert type identity, a different claim.
func Implements[I any](tb testing.TB, typ any) {
	tb.Helper()
	ifaceType := reflect.TypeFor[I]()
	if ifaceType.Kind() != reflect.Interface {
		tb.Fatalf("structural.Implements: type parameter must be an interface, got %s", ifaceType)
		return
	}
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
