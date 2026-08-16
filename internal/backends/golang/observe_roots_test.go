package golang

import (
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// The observation bracket's extra roots: the package's in-tree
// import-closure directories first, the invocation's reviewed bracket
// paths after; an unresolved closure — the listing failed, or the
// package has no entry — refuses with a stated reason instead of
// sealing only the package directory silently
// (REQ-evidence-witness-freshness's consuming-compile seal).
func TestGoBracketRootsDeclareImportClosure(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	n := &NormalizedInvocation{
		BracketPaths: []string{"docs/corpus"},
		PkgClosureDirs: map[string][]string{
			"example.com/m/app":  {"deep", "lib"},
			"example.com/m/leaf": {},
		},
	}
	roots, refused := bracketRootsFor(n, "example.com/m/app")
	if refused != "" {
		t.Fatalf("resolved closure refused: %s", refused)
	}
	if want := []string{"deep", "lib", "docs/corpus"}; !slices.Equal(roots, want) {
		t.Fatalf("roots = %v, want closure dirs then reviewed paths %v", roots, want)
	}
	roots, refused = bracketRootsFor(n, "example.com/m/leaf")
	if refused != "" {
		t.Fatalf("empty closure refused: %s", refused)
	}
	if want := []string{"docs/corpus"}; !slices.Equal(roots, want) {
		t.Fatalf("leaf roots = %v, want reviewed paths only %v", roots, want)
	}
	if _, refused = bracketRootsFor(n, "example.com/m/unknown"); refused == "" {
		t.Fatal("unknown closure did not refuse")
	}
	failed := &NormalizedInvocation{ClosureDirsErr: "go list exploded"}
	if _, refused = bracketRootsFor(failed, "example.com/m/app"); !strings.Contains(refused, "go list exploded") {
		t.Fatalf("failed closure listing refusal does not name the cause: %q", refused)
	}
}
