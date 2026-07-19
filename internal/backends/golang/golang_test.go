package golang

import (
	"context"
	"go/types"
	"os"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// The backend is tested against this module itself: the repository's own
// symbols are the fixture, exactly as the corpus is the compiler's.
var backend = func() *Backend {
	b, err := New("../../..")
	if err != nil {
		panic(err)
	}
	return b
}()

const mod = "github.com/greatliontech/stipulator"

// Deliberately not //gofresh:pure: the verdict depends on module
// sources outside this binary's closure, loaded through the shared
// backend at package init — before the testlog starts, so no digest
// guards them. The witness re-runs every gate.
func TestResolve(t *testing.T) {
	cases := []struct {
		name, symbol string
		want         verify.Resolution
	}{
		{"exported func", mod + "/internal/corpus.LoadManifest", verify.Resolved},
		{"unexported func", mod + "/internal/corpus.matchGlob", verify.Resolved},
		{"exported type", mod + "/internal/records.Store", verify.Resolved},
		{"unexported type", mod + "/internal/compile.termMatcher", verify.Resolved},
		{"const", mod + "/internal/profile.IDPattern", verify.Resolved},
		{"method on unexported type", mod + "/internal/profile.transformer.Transform", verify.Resolved},
		{"unexported method", mod + "/internal/profile.transformer.paragraph", verify.Resolved},
		{"test function", mod + "/internal/corpus.TestLoadManifest", verify.Resolved},
		{"fuzz function", mod + "/internal/canon.FuzzTextProjection", verify.Resolved},
		{"missing ident", mod + "/internal/corpus.NoSuchThing", verify.NotFound},
		{"missing package", mod + "/internal/nosuch.Thing", verify.NotFound},
		{"missing method", mod + "/internal/profile.transformer.NoSuch", verify.NotFound},
		{"generated symbol", mod + "/gen/stipulator/v1.Manifest", verify.GeneratedFile},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, shape, err := backend.Resolve(c.symbol)
			if err != nil {
				t.Fatal(err)
			}
			if res != c.want {
				t.Fatalf("Resolve(%s) = %v, want %v", c.symbol, res, c.want)
			}
			if res == verify.Resolved && len(shape) != 64 {
				t.Fatalf("shape hash %q", shape)
			}
		})
	}
}

// TestFixtureModule exercises the branches this repository's own healthy
// tree cannot: external test packages, generated-type promotion through
// embedding, interface methods, and packages that fail to load.
//
//gofresh:pure
func TestFixtureModule(t *testing.T) {
	b, err := New("testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, symbol string
		want         verify.Resolution
	}{
		{"plain function", "example.com/fixture/lib.F", verify.Resolved},
		{"external test package function", "example.com/fixture/lib.TestExt", verify.Resolved},
		{"generated type", "example.com/fixture/genp.G", verify.GeneratedFile},
		{"generated method", "example.com/fixture/genp.G.M", verify.GeneratedFile},
		{"promoted method from generated embed", "example.com/fixture/lib.W.M", verify.GeneratedFile},
		{"wrapper type itself is hand-written", "example.com/fixture/lib.W", verify.Resolved},
		{"interface method", "example.com/fixture/lib.I.M", verify.Resolved},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, _, err := b.Resolve(c.symbol)
			if err != nil {
				t.Fatal(err)
			}
			if res != c.want {
				t.Fatalf("Resolve(%s) = %v, want %v", c.symbol, res, c.want)
			}
		})
	}
	t.Run("load failure is an error, not an absence", func(t *testing.T) {
		_, _, err := b.Resolve("example.com/fixture/broken.F")
		if err == nil {
			t.Fatal("broken package read as absence")
		}
	})
}

// TestShapeHashIsPackageQualified pins that identically-named,
// identically-shaped symbols in different packages hash differently: the
// rendering must carry full package paths, or cross-package shape drift
// becomes invisible.
//
//gofresh:pure
func TestShapeHashIsPackageQualified(t *testing.T) {
	fn := func(path string) *types.Func {
		pkg := types.NewPackage(path, "p")
		local := types.NewNamed(types.NewTypeName(0, pkg, "T", nil), types.Typ[types.Int], nil)
		sig := types.NewSignatureType(nil, nil, nil,
			types.NewTuple(types.NewVar(0, pkg, "t", local)), nil, false)
		return types.NewFunc(0, pkg, "F", sig)
	}
	if shapeHash(fn("example.com/a")) == shapeHash(fn("example.com/b")) {
		t.Fatal("shape hash not package-qualified: cross-package collision")
	}
}

// Deliberately not //gofresh:pure: the verdict depends on module
// sources outside this binary's closure, loaded through the shared
// backend at package init — before the testlog starts, so no digest
// guards them. The witness re-runs every gate.
func TestShapeHashDistinguishesSignatures(t *testing.T) {
	_, a, err := backend.Resolve(mod + "/internal/corpus.LoadManifest")
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := backend.Resolve(mod + "/internal/corpus.Enumerate")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("distinct signatures share a shape hash")
	}
	_, a2, err := backend.Resolve(mod + "/internal/corpus.LoadManifest")
	if err != nil {
		t.Fatal(err)
	}
	if a != a2 {
		t.Fatal("shape hash not stable across resolutions")
	}
}

// TestWitnessClass pins property-vs-example classification: fuzz targets
// and rapid-driven tests are property witnesses, ordinary tests are
// example witnesses, resolved from the code, never declared.
//
// Deliberately not //gofresh:pure: the verdict depends on module
// sources outside this binary's closure, loaded through the shared
// backend at package init — before the testlog starts, so no digest
// guards them. The witness re-runs every gate.
func TestWitnessClass(t *testing.T) {
	if got := backend.WitnessClass(mod + "/internal/canon.FuzzTextProjection"); got != verify.PropertyWitness {
		t.Fatalf("fuzz target classified %v", got)
	}
	if got := backend.WitnessClass(mod + "/internal/corpus.TestLoadManifest"); got != verify.ExampleWitness {
		t.Fatalf("ordinary test classified %v", got)
	}
	if got := backend.WitnessClass(mod + "/internal/corpus.LoadManifest"); got != verify.ExampleWitness {
		t.Fatalf("non-test symbol classified %v", got)
	}

	// The rapid check drivers quantify; generator construction alone does
	// not.
	fb := fixtureBackend(t)
	if got := fb.WitnessClass("example.com/fixture/lib.TestPropRapidCheck"); got != verify.PropertyWitness {
		t.Fatalf("rapid.Check test classified %v", got)
	}
	if got := fb.WitnessClass("example.com/fixture/lib.TestPropRapidMakeCheck"); got != verify.PropertyWitness {
		t.Fatalf("rapid.MakeCheck test classified %v", got)
	}
	if got := fb.WitnessClass("example.com/fixture/lib.TestPropRapidGeneratorOnly"); got != verify.ExampleWitness {
		t.Fatalf("generator-only test classified %v", got)
	}
}

// TestSlice pins the slice facts: the seed's declaration plus the named
// module-local types its signature reaches, transitively, shape-pinned and
// canonically ordered — and nothing from outside the module.
//
// Deliberately not //gofresh:pure: the verdict depends on module
// sources outside this binary's closure, loaded through the shared
// backend at package init — before the testlog starts, so no digest
// guards them. The witness re-runs every gate.
func TestSlice(t *testing.T) {
	stipulate.Covers(t, "REQ-go-slice")
	decls, err := backend.Slice([]string{mod + "/internal/corpus.LoadManifest"})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, d := range decls {
		names[d.Package+"."+d.Name] = d.Declaration
		if len(d.ShapeHash) != 64 {
			t.Fatalf("decl not shape-pinned: %+v", d)
		}
	}
	// The function itself.
	if _, ok := names[mod+"/internal/corpus.LoadManifest"]; !ok {
		t.Fatalf("seed declaration missing: %v", names)
	}
	// Its result type, declared in the generated package — reached
	// transitively through the signature.
	if _, ok := names[mod+"/gen/stipulator/v1.Manifest"]; !ok {
		t.Fatalf("transitive named type missing: %v", names)
	}
	// Module-external types (io/fs.FS) appear only inside declaration
	// strings, never as declarations of their own.
	for key := range names {
		if strings.HasPrefix(key, "io/fs.") {
			t.Fatalf("module-external declaration leaked: %s", key)
		}
	}
	// Canonical order.
	for i := 1; i < len(decls); i++ {
		a, b := decls[i-1], decls[i]
		if a.Package > b.Package || (a.Package == b.Package && a.Name > b.Name) {
			t.Fatal("slice not canonically ordered")
		}
	}
}

// TestWorkspaceMembers pins the workspace walk: with a go.work present,
// every member's symbols resolve and every member's tests are witnessed —
// package patterns are module-scoped, so without the walk a nested
// published module silently vanishes from verification.
//
//gofresh:pure
func TestWorkspaceMembers(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-go-static-binding", "REQ-go-witness", "REQ-go-workspace")
	b, err := New("testdata/workspacemod")
	if err != nil {
		t.Fatal(err)
	}
	if res, _, err := b.Resolve("example.com/ws.Root"); err != nil || res != verify.Resolved {
		t.Fatalf("root member symbol: %v %v", res, err)
	}
	if res, _, err := b.Resolve("example.com/ws/sub.Nested"); err != nil || res != verify.Resolved {
		t.Fatalf("nested member symbol: %v %v", res, err)
	}

	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS("testdata/workspacemod")); err != nil {
		t.Fatal(err)
	}
	// The workspace's accepted policy is one race invocation per member —
	// package patterns are module-scoped, so a single `./...` invocation
	// would silently drop the nested member from witnessing.
	derived, err := DerivePolicy(tmp)
	if err != nil {
		t.Fatal(err)
	}
	writePolicyRecord(t, tmp, derived)
	firstFresh, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, firstFresh, "workspace first run")
	if firstFresh.Fresh != 0 || firstFresh.Ran != 2 {
		t.Fatalf("workspace first run: ran=%d fresh=%d, want both tests run", firstFresh.Ran, firstFresh.Fresh)
	}
	if firstFresh.Outcomes["example.com/ws.TestRoot"] != verify.TestPassed {
		t.Fatalf("root member unwitnessed: %v", firstFresh.Outcomes)
	}
	if firstFresh.Outcomes["example.com/ws/sub.TestNested"] != verify.TestPassed {
		t.Fatalf("nested member unwitnessed: %v", firstFresh.Outcomes)
	}
	found := false
	for _, r := range firstFresh.Registrations {
		if r.Package == "example.com/ws/sub" && r.Requirement == "REQ-ws-a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("nested member registration lost: %v", firstFresh.Registrations)
	}
	secondFresh, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, secondFresh, "workspace second run")
	if secondFresh.Ran != 0 || secondFresh.Fresh != 2 {
		t.Fatalf("workspace second run: ran=%d fresh=%d, want both member tests served", secondFresh.Ran, secondFresh.Fresh)
	}

	// A member escaping the tree is refused: hermeticity, never bent.
	if _, err := New("testdata/escapemod"); err == nil || !strings.Contains(err.Error(), "escapes the verification tree") {
		t.Fatalf("escaping go.work member accepted: %v", err)
	}
}
