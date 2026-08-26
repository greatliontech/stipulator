package golang

import (
	"context"
	"slices"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestGoDiscoveryEnumeratesCompleteObligationSet pins the enumeration
// exhaustively against the fixture workspace: named tests, external
// tests, executable examples (and only those), fuzz targets with their
// committed seeds, and packages with no runnable test all appear; nothing
// else does.
func TestGoDiscoveryEnumeratesCompleteObligationSet(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-conservation")
	neutralAmbient(t)
	dir := discoverFixture(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("root", cfg))
	if err != nil {
		t.Fatal(err)
	}
	got, err := DiscoverInvocation(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, o := range got {
		ids = append(ids, o.ID())
	}
	want := []string{
		"example:example.com/disc/alpha.ExampleGreet",
		"fuzz:example.com/disc/beta.FuzzBeta",
		"package:example.com/disc/alpha",
		"package:example.com/disc/beta",
		"package:example.com/disc/notest",
		"package:example.com/disc/tagged",
		"seed:example.com/disc/beta.FuzzBeta/seed-a",
		"test:example.com/disc/alpha.TestAlpha",
		"test:example.com/disc/alpha.TestExternal",
	}
	if !slices.Equal(ids, want) {
		t.Errorf("obligations = %q, want %q", ids, want)
	}
}

// TestGoDiscoveryBuildSelectionChangesObligations pins that tags move the
// selection exactly as they move a direct `go test` of the same scope: the
// build-tagged test exists only under its tag.
func TestGoDiscoveryBuildSelectionChangesObligations(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-conservation")
	neutralAmbient(t)
	dir := discoverFixture(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./tagged"})
	cfg.SetTags([]string{"special"})
	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("tagged", cfg))
	if err != nil {
		t.Fatal(err)
	}
	got, err := DiscoverInvocation(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, o := range got {
		ids = append(ids, o.ID())
	}
	want := []string{
		"package:example.com/disc/tagged",
		"test:example.com/disc/tagged.TestTagged",
	}
	if !slices.Equal(ids, want) {
		t.Errorf("tagged obligations = %q, want %q", ids, want)
	}
}

// TestGoDiscoveryWorkspaceMemberScope pins the nested member's own
// discovery: module roots scope the selection, so a member invocation
// enumerates exactly its own obligations.
func TestGoDiscoveryWorkspaceMemberScope(t *testing.T) {
	stipulate.Covers(t, "REQ-go-workspace")
	neutralAmbient(t)
	dir := discoverFixture(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetModuleRoot("sub")
	cfg.SetPackages([]string{"./..."})
	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("race:sub", cfg))
	if err != nil {
		t.Fatal(err)
	}
	got, err := DiscoverInvocation(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, o := range got {
		ids = append(ids, o.ID())
	}
	want := []string{
		"package:example.com/sub",
		"test:example.com/sub.TestSub",
	}
	if !slices.Equal(ids, want) {
		t.Errorf("member obligations = %q, want %q", ids, want)
	}
}

// Discovery records each package's in-tree test-build import-closure
// directories: the observation bracket's closure roots cover production
// imports, test-only imports, and their transitive dependencies, while
// the package's own directory and out-of-tree dependencies stay out
// (REQ-evidence-witness-freshness's consuming-compile seal).
func TestGoDiscoveryRecordsClosureDirs(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":       "module example.com/clo\n\ngo 1.26\n",
		"deep/deep.go": "package deep\n\nfunc Deep() {}\n",
		"lib/lib.go":   "package lib\n\nimport \"example.com/clo/deep\"\n\nfunc Lib() { deep.Deep() }\n",
		"tdep/tdep.go": "package tdep\n\nfunc TDep() {}\n",
		"app/app.go":   "package app\n\nimport \"example.com/clo/lib\"\n\nfunc App() { lib.Lib() }\n",
		"app/app_test.go": `package app

import (
	"testing"

	"example.com/clo/tdep"
)

func TestApp(t *testing.T) { tdep.TDep(); App() }
`,
		"app/ext_test.go": `package app_test

import (
	"testing"

	"example.com/clo/app"
)

func TestExt(t *testing.T) { app.App() }
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("root", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverInvocation(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if n.ClosureDirsErr != "" {
		t.Fatalf("closure listing failed: %s", n.ClosureDirsErr)
	}
	got := n.PkgClosureDirs["example.com/clo/app"]
	want := map[string]bool{"deep": true, "lib": true, "tdep": true}
	gotSet := map[string]bool{}
	for _, rel := range got {
		gotSet[rel] = true
	}
	for rel := range want {
		if !gotSet[rel] {
			t.Errorf("app closure missing %q: %v", rel, got)
		}
	}
	if gotSet["app"] {
		t.Errorf("app closure includes its own directory: %v", got)
	}
	for _, rel := range got {
		if strings.HasPrefix(rel, "/") {
			t.Errorf("closure root %q is not tree-relative", rel)
		}
	}
	// A leaf package's closure is empty but present: the bracket
	// declares an explicitly empty closure, never an unknown one.
	if rels, ok := n.PkgClosureDirs["example.com/clo/deep"]; !ok {
		t.Error("leaf package has no closure entry")
	} else if len(rels) != 0 {
		t.Errorf("leaf closure = %v, want empty", rels)
	}
}
