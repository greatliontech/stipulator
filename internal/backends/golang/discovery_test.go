package golang

import (
	"context"
	"slices"
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
