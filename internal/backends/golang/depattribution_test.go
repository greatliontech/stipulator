package golang

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestGoPinTableClasses pins the committed-pin rendering per class:
// the replace class outranks a require for the same module, a
// version-scoped replace carries its condition, disagreeing member
// requires are all named with the selection left to the toolchain, the
// member class names the workspace directory, and a module no
// committed pin speaks for answers false.
//
// This subject parses committed fixture files: its inputs are the
// source closure the fingerprint already pins, asserted pure under
// REQ-purity-responsibility.
//
//gofresh:pure
func TestGoPinTableClasses(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	broken := loadPinTable("testdata/depbroken")
	if desc, ok := broken.pin("example.com/dep"); !ok || !strings.Contains(desc, "replaced to ./dep (go.mod)") {
		t.Errorf("replace class = %q, %v; want the replace outranking the require", desc, ok)
	}
	ws := loadPinTable("testdata/depws")
	desc, ok := ws.pin("example.com/ext/sub")
	if !ok {
		t.Fatal("require class answered false")
	}
	for _, want := range []string{"v1.2.3 (a/go.mod require)", "v1.5.0 (b/go.mod require)", "minimal version selection"} {
		if !strings.Contains(desc, want) {
			t.Errorf("require class %q does not name %q", desc, want)
		}
	}
	if desc, ok := ws.pin("example.com/depws/b/inner"); !ok || !strings.Contains(desc, `workspace member "b"`) {
		t.Errorf("member class = %q, %v; want the workspace member named", desc, ok)
	}
	if desc, ok := ws.pin("example.com/solo/pkg"); !ok || !strings.Contains(desc, "module example.com/solo at v1.4.0 (a/go.mod require)") {
		t.Errorf("single-require class = %q, %v; want the one directive named", desc, ok)
	}
	// A version-scoped replace with no require beside it still names
	// its condition and states the absence.
	if desc, ok := ws.pin("example.com/old"); !ok || !strings.Contains(desc, "applying at v1.0.0 only") || !strings.Contains(desc, "no require directive beside it") {
		t.Errorf("version-scoped replace alone = %q, %v; want its condition and the require absence", desc, ok)
	}
	// An inert version-scoped replace renders BESIDE the require that
	// actually resolves the import — never instead of it.
	depsc, ok := ws.pin("example.com/pinned")
	if !ok || !strings.Contains(depsc, "at v1.4.0 (a/go.mod require)") || !strings.Contains(depsc, "beside replaced to example.com/patched@v1.0.1 (a/go.mod, applying at v1.0.0 only)") {
		t.Errorf("conditional-replace-beside-require = %q, %v; want both directives named", depsc, ok)
	}
	// An all-versions replace supersedes the requires but composes
	// with the version-scoped replace that outranks it at its version
	// — the toolchain consults the exact-version directive first, so
	// withholding it would name the directive that loses.
	wild, ok := ws.pin("example.com/wild")
	if !ok || !strings.Contains(wild, "replaced to example.com/fork@v1.0.0 (a/go.mod)") || !strings.Contains(wild, "beside replaced to example.com/patch@v1.0.1 (a/go.mod, applying at v1.0.0 only)") {
		t.Errorf("wildcard-plus-scoped = %q, %v; want both replaces named", wild, ok)
	}
	if _, ok := ws.pin("example.com/nope"); ok {
		t.Error("a module no committed pin speaks for must answer false")
	}
}

// TestGoMemberModuleNestedIsDeterministic pins the owner rule on
// nested member modules: the longest owning member answers, never
// map-iteration order — the same commit must yield the same
// attribution on every run.
//
//gofresh:pure
func TestGoMemberModuleNestedIsDeterministic(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	table := &pinTable{members: map[string]string{
		"example.com/m":     ".",
		"example.com/m/sub": "sub",
	}}
	for range 200 {
		if mod, ok := table.memberModule("example.com/m/sub/x"); !ok || mod != "example.com/m/sub" {
			t.Fatalf("memberModule(nested) = %q, %v; want the longest owning member every run", mod, ok)
		}
	}
}

// TestGoModuleOwnsPathBoundary pins the one prefix rule every owner
// question shares: ownership holds only at a path boundary, so a
// sibling whose path merely extends the module's ("b" vs "bb") is
// never owned, while the module itself and its subpackages are.
//
//gofresh:pure
func TestGoModuleOwnsPathBoundary(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	cases := []struct {
		mod, path string
		want      bool
	}{
		{"example.com/depws/b", "example.com/depws/b", true},
		{"example.com/depws/b", "example.com/depws/b/x", true},
		{"example.com/depws/b", "example.com/depws/bb", false},
		{"example.com/depws/b", "example.com/depws/bb/x", false},
		{"example.com/m", "example.com/m_test", false},
		{"example.com/m", "example.com/mother/pkg", false},
	}
	for _, c := range cases {
		if got := moduleOwns(c.mod, c.path); got != c.want {
			t.Errorf("moduleOwns(%q, %q) = %v, want %v", c.mod, c.path, got, c.want)
		}
	}
}

// TestGoCondReplacesBySemverOrder pins the version-scoped replace
// ordering: conditions render in their applying-version's semantic
// precedence, never lexicographic and never the rendered string's
// target order.
//
//gofresh:pure
func TestGoCondReplacesBySemverOrder(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	got := condReplacesBySemver([]string{
		"replaced to example.com/aaa@v0.1.0 (go.mod, applying at v1.10.0 only)",
		"replaced to example.com/zzz@v0.1.0 (go.mod, applying at v1.9.0 only)",
	})
	if !strings.Contains(got[0], "applying at v1.9.0") || !strings.Contains(got[1], "applying at v1.10.0") {
		t.Errorf("condition order = %v; want v1.9.0 before v1.10.0 regardless of target order", got)
	}
}

// TestGoRequiresBySemverOrder pins the multi-require ordering: the
// directives render in semantic version precedence — the order minimal
// version selection reasons in — never lexicographic, where v1.10.0
// would sort before v1.9.0.
//
//gofresh:pure
func TestGoRequiresBySemverOrder(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	got := requiresBySemver([]string{"v1.10.0 (b/go.mod require)", "v1.9.0 (a/go.mod require)"})
	if got[0] != "v1.9.0 (a/go.mod require)" || got[1] != "v1.10.0 (b/go.mod require)" {
		t.Errorf("semver order = %v; want v1.9.0 before v1.10.0", got)
	}
}

// TestGoLoaderTextRendering pins the loader-error rendering: compiler
// framing lines are dropped, remaining lines join single-line, a real
// loader position prefixes the message, and the wrapped shape's "-"
// position does not.
//
//gofresh:pure
func TestGoLoaderTextRendering(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	wrapped := packages.Error{Pos: "-", Msg: "# example.com/p\n./x.go:1:1: undefined: q.R\nmore context\n"}
	if got := loaderText(wrapped); got != "./x.go:1:1: undefined: q.R; more context" {
		t.Errorf("wrapped shape = %q; want framing dropped and lines joined", got)
	}
	positioned := packages.Error{Pos: "/abs/x.go:3:7", Msg: "undefined: q.R"}
	if got := loaderText(positioned); got != "/abs/x.go:3:7: undefined: q.R" {
		t.Errorf("positioned shape = %q; want the loader's position kept", got)
	}
}

// TestGoMemberModuleKeepsTestSuffixedMemberRoot pins the fold's
// ordering: a member module whose own path ends in "_test" identifies
// its root package — the untrimmed path matches before the xtest fold
// could erase it.
//
//gofresh:pure
func TestGoMemberModuleKeepsTestSuffixedMemberRoot(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	table := &pinTable{members: map[string]string{"example.com/m_test": "."}}
	if mod, ok := table.memberModule("example.com/m_test"); !ok || mod != "example.com/m_test" {
		t.Errorf("memberModule(root of a _test-suffixed member) = %q, %v; want the member identified", mod, ok)
	}
}

// TestGoMemberModuleFoldsExternalTestSuffix pins the xtest fold: a
// module-root external test package ("<module>_test") identifies its
// module, so an in-tree defect inside it is never attributed to a pin.
//
// This subject parses committed fixture files: its inputs are the
// source closure the fingerprint already pins, asserted pure under
// REQ-purity-responsibility.
//
//gofresh:pure
func TestGoMemberModuleFoldsExternalTestSuffix(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	table := loadPinTable("testdata/depbroken")
	if mod, ok := table.memberModule("example.com/depbroken_test"); !ok || mod != "example.com/depbroken" {
		t.Errorf("memberModule(xtest) = %q, %v; want the base module identified", mod, ok)
	}
	if _, ok := table.memberModule("example.com/foreign"); ok {
		t.Error("a package outside every member identified a module")
	}
}

// TestGoDependencyImportMissingPackageShapes pins the go command's
// missing-package message classes: each shape yields the named package
// path, and a path inside the erroring package's own module never
// classifies as a dependency.
//
//gofresh:pure
func TestGoDependencyImportMissingPackageShapes(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	pkg := &packages.Package{PkgPath: "example.com/m/p"}
	cases := []struct {
		msg  string
		want string
	}{
		{"no required module provides package example.com/z/q", "example.com/z/q"},
		{"missing go.sum entry for module providing package example.com/z/q", "example.com/z/q"},
		{`cannot find package "example.com/z/q"`, "example.com/z/q"},
	}
	for _, c := range cases {
		imp, ok := dependencyImport(pkg, packages.Error{Msg: c.msg}, "example.com/m")
		if !ok || imp != c.want {
			t.Errorf("dependencyImport(%q) = %q, %v; want %q", c.msg, imp, ok, c.want)
		}
	}
	if imp, ok := dependencyImport(pkg, packages.Error{Msg: "no required module provides package example.com/m/other"}, "example.com/m"); ok {
		t.Errorf("own-module package %q classified as a dependency", imp)
	}
	if _, ok := dependencyImport(pkg, packages.Error{Msg: "syntax error: unexpected }"}, "example.com/m"); ok {
		t.Error("an unrecognized shape classified")
	}
}

// TestGoImportForIdentRules pins the identifier walk: a named import
// binds its name, a plain import falls back to the loader-known
// package name and then the path's last element, and an identifier
// binding two distinct paths across the package's files refuses rather
// than guessing.
//
//gofresh:pure
func TestGoImportForIdentRules(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	parse := func(name, src string) *ast.File {
		t.Helper()
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	f1 := parse("a.go", "package p\nimport z \"example.com/x\"\nimport \"example.com/y/last\"\n")
	f2 := parse("b.go", "package p\nimport z \"example.com/other\"\n")
	pkg := &packages.Package{
		Syntax:  []*ast.File{f1},
		Imports: map[string]*packages.Package{"example.com/y/last": {Name: "renamed"}},
	}
	if imp, ok := importForIdent(pkg, "z"); !ok || imp != "example.com/x" {
		t.Errorf("named import: got %q, %v; want example.com/x", imp, ok)
	}
	if imp, ok := importForIdent(pkg, "renamed"); !ok || imp != "example.com/y/last" {
		t.Errorf("loader-known package name: got %q, %v; want example.com/y/last", imp, ok)
	}
	bare := &packages.Package{Syntax: []*ast.File{f1}}
	if imp, ok := importForIdent(bare, "last"); !ok || imp != "example.com/y/last" {
		t.Errorf("last-element fallback: got %q, %v; want example.com/y/last", imp, ok)
	}
	ambiguous := &packages.Package{Syntax: []*ast.File{f1, f2}}
	if imp, ok := importForIdent(ambiguous, "z"); ok {
		t.Errorf("ambiguous identifier answered %q; want a refusal", imp)
	}
}

// TestGoLoadAttributionNoCommittedPinArm pins the arm for an import no
// committed pin speaks for: the state is still classified — the import
// named, the absence of a pin stated — never a guessed directive.
//
// This subject analyzes a committed fixture tree: its inputs are the
// source closure and guard-covered toolchain state the fingerprint
// already pins, asserted pure under REQ-purity-responsibility.
//
//gofresh:pure
func TestGoLoadAttributionNoCommittedPinArm(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	b, err := newContext(context.Background(), "testdata/depbroken")
	if err != nil {
		t.Fatal(err)
	}
	pkg := &packages.Package{
		PkgPath: "example.com/depbroken",
		Errors:  []packages.Error{{Msg: "no required module provides package example.com/unpinned/x"}},
	}
	msg, ok := b.loadErrorAttribution(pkg)
	if !ok || !strings.Contains(msg, `import "example.com/unpinned/x" is resolved by no committed pin`) {
		t.Errorf("no-pin arm = %q, %v; want the pin absence stated", msg, ok)
	}
}

// TestGoLoadAttributionFailsClosedOnUnidentifiedModule pins the guard:
// a package whose own module no member matches gets no attribution at
// all — the raw diagnostic surfaces rather than a guess.
//
// This subject analyzes a committed fixture tree: its inputs are the
// source closure and guard-covered toolchain state the fingerprint
// already pins, asserted pure under REQ-purity-responsibility.
//
//gofresh:pure
func TestGoLoadAttributionFailsClosedOnUnidentifiedModule(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	b, err := newContext(context.Background(), "testdata/depbroken")
	if err != nil {
		t.Fatal(err)
	}
	pkg := &packages.Package{
		PkgPath: "example.com/foreign/pkg",
		Errors:  []packages.Error{{Msg: "no required module provides package example.com/unpinned/x"}},
	}
	if msg, ok := b.loadErrorAttribution(pkg); ok {
		t.Errorf("unidentified own module still attributed: %q", msg)
	}
}

// TestGoLoadAttributionMemberClass pins the member class over a real
// workspace load: a member referencing a symbol a sibling member does
// not declare refuses with the member pin named — the working copy is
// what lacks the surface — and the message stays single-line for the
// row-oriented renderers.
//
// This subject analyzes a committed fixture tree: its inputs are the
// source closure and guard-covered toolchain state the fingerprint
// already pins, asserted pure under REQ-purity-responsibility.
//
//gofresh:pure
func TestGoLoadAttributionMemberClass(t *testing.T) {
	stipulate.Covers(t, "REQ-go-load-attribution")
	b, err := newContext(context.Background(), "testdata/depws")
	if err != nil {
		t.Fatal(err)
	}
	_, _, rerr := b.Resolve("example.com/depws/a.Use")
	if rerr == nil {
		t.Fatal("a load-errored package must refuse, never answer")
	}
	for _, want := range []string{
		"dependency-resolution state",
		`import "example.com/depws/b"`,
		`workspace member "b"`,
		"undefined: b.Gone",
	} {
		if !strings.Contains(rerr.Error(), want) {
			t.Errorf("refusal %q does not name %q", rerr.Error(), want)
		}
	}
	if strings.Contains(rerr.Error(), "\n") {
		t.Errorf("attribution message is not single-line: %q", rerr.Error())
	}
}
