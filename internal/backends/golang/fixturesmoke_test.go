package golang

import (
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
)

// The Backend's resolution surfaces keep fast-tier coverage over the
// hermetic fixture module — the repository-tree backend the other
// resolution tests read loads only on the full tier, so without this
// smoke the fast tier would exercise neither Slice nor ReachedPackages,
// and Resolve/SymbolFile only through the build-selection tests
// (REQ-go-static-binding, REQ-go-slice).
func TestFixtureBackendResolvesSlicesAndLocates(t *testing.T) {
	fb := fixtureBackend(t)
	res, shape, err := fb.Resolve("example.com/fixture/lib.Add")
	if err != nil || res != verify.Resolved || shape == "" {
		t.Fatalf("Resolve(lib.Add) = %v %q %v, want resolved with a shape", res, shape, err)
	}
	if res, _, err := fb.Resolve("example.com/fixture/lib.NoSuch"); err != nil || res != verify.NotFound {
		t.Fatalf("Resolve(lib.NoSuch) = %v %v, want not found", res, err)
	}
	decls, err := fb.Slice([]string{"example.com/fixture/lib.Add"})
	if err != nil || len(decls) == 0 || decls[0].Name != "Add" || decls[0].ShapeHash != shape {
		t.Fatalf("Slice(lib.Add) = %+v %v, want the seed declaration with its shape", decls, err)
	}
	file, found := fb.SymbolFile("example.com/fixture/lib.Add")
	if !found || !strings.HasSuffix(file, "lib/lib.go") {
		t.Fatalf("SymbolFile(lib.Add) = %q %t, want lib/lib.go", file, found)
	}
	if reach := fb.ReachedPackages([]string{file}); !reach["example.com/fixture/lib"] {
		t.Fatalf("ReachedPackages(%s) = %v, want lib itself", file, reach)
	}
}
