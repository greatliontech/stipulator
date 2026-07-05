package golang

import (
	"go/types"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
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
