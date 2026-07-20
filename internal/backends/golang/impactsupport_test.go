package golang

import (
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// The impact preview's code-side join stands on the declaring file: a
// symbol resolves to the tree-relative path of the file declaring it,
// and an unresolvable reference reports false instead of a guess.
//
// Deliberately not //gofresh:pure: the shared backend loads module
// sources outside this binary's closure at package init.
func TestSymbolFileNamesDeclaringFile(t *testing.T) {
	stipulate.Covers(t, "REQ-change-impact")
	cases := []struct {
		symbol string
		want   string
	}{
		{mod + "/internal/records.Load", "internal/records/records.go"},
		{mod + "/internal/records.Store", "internal/records/records.go"},
		{mod + "/internal/backends/golang.Backend.Resolve", "internal/backends/golang/golang.go"},
		{mod + "/internal/gitfs.Changed", "internal/gitfs/gitfs.go"},
	}
	for _, c := range cases {
		file, ok := backend.SymbolFile(c.symbol)
		if !ok || file != c.want {
			t.Errorf("SymbolFile(%s) = %q, %v; want %q", c.symbol, file, ok, c.want)
		}
	}
	if file, ok := backend.SymbolFile(mod + "/internal/records.NoSuchThing"); ok {
		t.Errorf("missing symbol resolved to %q", file)
	}
}

// Reach is the reflexive reverse-import closure: a change in canon
// reaches its importers (compile, the golang backend) and itself, and
// never a package with no import path to it.
//
// Deliberately not //gofresh:pure: same shared-backend load as above.
func TestReachedPackagesReverseClosure(t *testing.T) {
	stipulate.Covers(t, "REQ-change-impact")
	reach := backend.ReachedPackages([]string{"internal/canon/canon.go"})
	for _, want := range []string{
		mod + "/internal/canon",
		mod + "/internal/compile",
		mod + "/internal/backends/golang",
	} {
		if !reach[want] {
			t.Errorf("reach misses %s", want)
		}
	}
	// gitfs's production code never imports canon, but its test file
	// imports compile, which does: test variants fold into the reach,
	// because the package's witnesses are what the change would re-run.
	if !reach[mod+"/internal/gitfs"] {
		t.Error("reach misses gitfs, whose tests import compile")
	}
	if reach[mod+"/internal/wire"] {
		t.Error("reach includes wire, which has no import path to canon")
	}
	if len(backend.ReachedPackages([]string{"docs/specs/change.md"})) != 0 {
		t.Error("a non-Go file seeded a package reach")
	}
}
