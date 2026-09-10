package golang

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

// The one completion rule: a group is complete at its last covering
// invocation's completion, over its executing packages alone, an
// ambiguous package covering nothing; each group is returned once;
// a group with nothing executing is complete from the start.
//
//gofresh:pure
func TestGroupTrackerCompletesAtTheLastCoveringInvocation(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-cancellation")
	a := &captureGroup{tests: map[string][]string{"p": {"TestP"}, "q": {"TestQ"}, "amb": {"TestA"}}, pkgInv: map[string]string{"p": "one", "q": "two", "amb": "three"}, ambiguous: map[string]bool{"amb": true}}
	b := &captureGroup{tests: map[string][]string{"r": {"TestR"}}, pkgInv: map[string]string{"r": "one"}, ambiguous: map[string]bool{}}
	served := &captureGroup{tests: map[string][]string{"s": {"TestS"}}, pkgInv: map[string]string{"s": "two"}, ambiguous: map[string]bool{}}
	tracker := newGroupTracker([]*captureGroup{a, b, served}, func(g *captureGroup, pkg string) bool { return g != served })
	if len(tracker.pending[served]) != 0 || len(tracker.pending[a]) == 0 || len(tracker.pending[b]) == 0 {
		t.Fatal("initial coverage: a group with nothing executing waits on no invocation, the others wait")
	}
	if ready := tracker.invocationDone("one"); len(ready) != 1 || ready[0] != b {
		t.Fatalf("after one: ready %v, want b alone (a still waits on two)", ready)
	}
	// The ambiguous package's invocation never completed: a is ready
	// on its non-ambiguous packages alone.
	if ready := tracker.invocationDone("two"); len(ready) != 1 || ready[0] != a {
		t.Fatalf("after two: ready %v, want a", ready)
	}
	if ready := tracker.invocationDone("three"); len(ready) != 0 {
		t.Fatalf("an ambiguous package's invocation completed a group: %v", ready)
	}
	if ready := tracker.invocationDone("one"); len(ready) != 0 {
		t.Fatalf("a completed invocation completed a group twice: %v", ready)
	}
	if !tracker.finish(served) || tracker.finish(served) || tracker.finish(a) {
		t.Fatal("finish marks a group once and never a group the invocations finished")
	}
}

// On the serving form a group's executing packages alone cover it: a
// group holding one served and one stale package persists at the
// stale package's invocation's completion — a cancellation at that
// note leaves the new record installed — where waiting on the served
// package's never-run invocation would defer the install to
// verification and lose it (REQ-policy-cancellation's unit of
// persistence, on the selective form).
//
// Deliberately not //gofresh:pure: executes the fixture's tests.
func TestServingFormPersistsAtTheExecutingInvocation(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-cancellation")
	if testing.Short() {
		t.Skip("executes race invocations over a temporary module")
	}
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tmp := writeModule(t, map[string]string{
		"go.mod":      "module example.com/units\n\ngo 1.26\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {}\n",
	})
	first := &stipulatorv1.GoInvocationConfig{}
	first.SetPackages([]string{"./a"})
	first.SetRace(true)
	second := &stipulatorv1.GoInvocationConfig{}
	second.SetPackages([]string{"./b"})
	second.SetRace(true)
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("first", first), goInvocation("second", second)})
	// One capture group (one environment), both packages recorded.
	ctx := progress.NewContext(context.Background(), progress.New(func(*stipulatorv1.ProgressEvent) {}, progress.WithInterval(time.Hour)))
	if _, err := RunWitnessesPolicy(ctx, mustCapture(t, ctx, tmp, pol), noSeeding{}); err != nil {
		t.Fatal(err)
	}
	// b's record goes stale; a's serves.
	if err := os.WriteFile(filepath.Join(tmp, "b", "b_test.go"), []byte("package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) { _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := len(witnesscache.Load(tmp))
	cctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var notes []string
	rep := progress.New(func(e *stipulatorv1.ProgressEvent) {
		if strings.HasPrefix(e.GetNote(), "persisted: ") {
			notes = append(notes, e.GetNote())
			cancel()
		}
	}, progress.WithInterval(time.Hour))
	cctx = progress.NewContext(cctx, rep)
	_, err := RunWitnessesPolicy(cctx, mustCapture(t, context.Background(), tmp, pol), noSeeding{})
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "persisted: second (") {
		t.Fatalf("persisted notes = %v (err %v); want the group installed at the executing invocation's completion", notes, err)
	}
	stale := false
	for _, rec := range witnesscache.Load(tmp) {
		if rec.Package == "example.com/units/b" && rec.Test == "TestB" {
			stale = true
		}
	}
	if !stale || len(witnesscache.Load(tmp)) < before {
		t.Fatalf("the stale package's new record is not in the store after the cancellation (%d records, was %d)", len(witnesscache.Load(tmp)), before)
	}
}

// The selective predicate keys the selection by the package's OWN
// covering invocation: a package another group's invocation names —
// served here, stale there — executes nothing for this group, so its
// invocation never enters the covering set (REQ-policy-cancellation:
// the group persists at its last covering invocation, never later).
//
//gofresh:pure
func TestSelectedStalePackagesKeyByTheCoveringInvocation(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-cancellation")
	g1 := &captureGroup{tests: map[string][]string{"p": {"TestP"}, "q": {"TestQ"}},
		pkgInv: map[string]string{"p": "x1", "q": "x2"}, ambiguous: map[string]bool{}}
	g2 := &captureGroup{tests: map[string][]string{"p": {"TestP"}},
		pkgInv: map[string]string{"p": "y1"}, ambiguous: map[string]bool{}}
	sel := map[string]TestSelection{"x2": {"q": {"TestQ"}}, "y1": {"p": {"TestP"}}}
	tracker := newGroupTracker([]*captureGroup{g1, g2}, selectedStalePackages(sel))
	want := map[*captureGroup]map[string]bool{g1: {"x2": true}, g2: {"y1": true}}
	for g, pending := range want {
		if got := tracker.pending[g]; !maps.Equal(got, pending) {
			t.Fatalf("pending = %v; want %v — p is named under y1, another group's invocation, so g1 waits on x2 alone", got, pending)
		}
	}
}

// A scoped run: a stale package the scope leaves out executes nothing
// and covers nothing, so the group still installs at its executing
// invocation's completion — a cancellation there keeps the in-scope
// record (REQ-policy-cancellation on the id-scoped pass).
//
// Deliberately not //gofresh:pure: executes the fixture's tests.
func TestScopedRunPersistsAtTheExecutingInvocation(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-cancellation")
	if testing.Short() {
		t.Skip("executes race invocations over a temporary module")
	}
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tmp := writeModule(t, map[string]string{
		"go.mod":      "module example.com/units\n\ngo 1.26\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {}\n",
	})
	first := &stipulatorv1.GoInvocationConfig{}
	first.SetPackages([]string{"./a"})
	first.SetRace(true)
	second := &stipulatorv1.GoInvocationConfig{}
	second.SetPackages([]string{"./b"})
	second.SetRace(true)
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("first", first), goInvocation("second", second)})
	// Both stale (nothing recorded yet); the scope names b alone.
	scope := map[gofresh.Subject]bool{{Package: "example.com/units/b", Symbol: "TestB"}: true}
	cctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var notes []string
	rep := progress.New(func(e *stipulatorv1.ProgressEvent) {
		if strings.HasPrefix(e.GetNote(), "persisted: ") {
			notes = append(notes, e.GetNote())
			cancel()
		}
	}, progress.WithInterval(time.Hour))
	cctx = progress.NewContext(cctx, rep)
	_, err := RunWitnessesScoped(cctx, mustCapture(t, context.Background(), tmp, pol), scope, noSeeding{})
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "persisted: second (") {
		t.Fatalf("persisted notes = %v (err %v); want the group installed at the executing invocation's completion", notes, err)
	}
	found := false
	for _, rec := range witnesscache.Load(tmp) {
		found = found || (rec.Package == "example.com/units/b" && rec.Test == "TestB")
	}
	if !found {
		t.Fatal("the in-scope record is not in the store after the cancellation")
	}
}

// A group whose every package two of its invocations select is no
// recorder group at all — nothing in it can publish — so its subjects'
// refusal is recorded at discovery, where the double selection is
// decided, and the account names it rather than the structural
// fallback (REQ-evidence-witness-freshness: the refused set is
// diagnosable).
//
// Deliberately not //gofresh:pure: executes the fixture's tests.
func TestDoublySelectedPackageIsRefusedAtDiscovery(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes two race invocations over a temporary module")
	}
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tmp := writeModule(t, map[string]string{
		"go.mod":      "module example.com/units\n\ngo 1.26\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
	})
	first := &stipulatorv1.GoInvocationConfig{}
	first.SetPackages([]string{"./a"})
	first.SetRace(true)
	second := &stipulatorv1.GoInvocationConfig{}
	second.SetPackages([]string{"./a"})
	second.SetRace(true)
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("first", first), goInvocation("second", second)})
	ctx := context.Background()
	_, tr, err := ExecutePolicyWitnessed(ctx, mustCapture(t, ctx, tmp, pol), noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	const want = reasonNoProducingLeg
	if got := tr.UncacheableReasons["example.com/units/a.TestA"]; got != want {
		t.Fatalf("a.TestA reason = %q; want %q — the discovery-time refusal, not the structural fallback", got, want)
	}
	if len(witnesscache.Load(tmp)) != 0 {
		t.Fatal("an ambiguous package published a record")
	}
}

// A mixed group on the full form: one package exactly one invocation
// selects, one two select. The group is populated by the first, so
// publishGroup runs on it and must skip the second — under the first
// selecting invocation's rows alone it would install a record for a
// subject with no producing leg — and the tracker covers the group by
// the first alone (REQ-evidence-witness-freshness,
// REQ-policy-cancellation).
//
// Deliberately not //gofresh:pure: executes the fixture's tests.
func TestMixedGroupPublishesOnlyItsSinglySelectedPackage(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-policy-cancellation")
	if testing.Short() {
		t.Skip("executes two race invocations over a temporary module")
	}
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tmp := writeModule(t, map[string]string{
		"go.mod":      "module example.com/units\n\ngo 1.26\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {}\n",
	})
	first := &stipulatorv1.GoInvocationConfig{}
	first.SetPackages([]string{"./a", "./b"})
	first.SetRace(true)
	second := &stipulatorv1.GoInvocationConfig{}
	second.SetPackages([]string{"./a"})
	second.SetRace(true)
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("first", first), goInvocation("second", second)})
	var notes []string
	rep := progress.New(func(e *stipulatorv1.ProgressEvent) {
		if strings.HasPrefix(e.GetNote(), "persisted: ") {
			notes = append(notes, e.GetNote())
		}
	}, progress.WithInterval(time.Hour))
	ctx := progress.NewContext(context.Background(), rep)
	_, tr, err := ExecutePolicyWitnessed(ctx, mustCapture(t, ctx, tmp, pol), noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.UncacheableReasons["example.com/units/a.TestA"]; got != reasonNoProducingLeg {
		t.Fatalf("a.TestA reason = %q; want %q", got, reasonNoProducingLeg)
	}
	var stored []string
	for _, rec := range witnesscache.Load(tmp) {
		stored = append(stored, rec.Package+"."+rec.Test)
	}
	if len(stored) != 1 || stored[0] != "example.com/units/b.TestB" {
		t.Fatalf("store holds %v; want b.TestB alone — the doubly selected package publishes nothing", stored)
	}
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "persisted: first (") {
		t.Fatalf("persisted notes = %v; want the group installed at the first invocation — its one executing package's", notes)
	}
}
