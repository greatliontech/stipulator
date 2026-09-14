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
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/types/known/durationpb"
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

// Examples execute but never enter the freshness cache: the executed
// count excludes them on both forms, so the uncacheable number a run
// reports is a number a warm cache can drive to zero
// (REQ-evidence-witness-freshness's diagnosable set is over executed
// subjects).
//
// Deliberately not //gofresh:pure: executes the fixture's tests.
func TestExecutedCountExcludesExamplesOnBothForms(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes race invocations over a temporary module")
	}
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	files := map[string]string{
		"go.mod":      "module example.com/units\n\ngo 1.26\n",
		"a/a.go":      "package a\n\n// A is the example's subject.\nfunc A() string { return \"a\" }\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n\n// ExampleA runs (an Output comment, empty) without an ambient effect\n// that would refuse the compartment's other witness.\nfunc ExampleA() {\n\t_ = A()\n\t// Output:\n}\n",
	}
	// The race invocation is the witness leg; the plain one runs the
	// package whole on the selective form too, so the example executes
	// there as well.
	race := &stipulatorv1.GoInvocationConfig{}
	race.SetPackages([]string{"./a"})
	race.SetRace(true)
	plain := &stipulatorv1.GoInvocationConfig{}
	plain.SetPackages([]string{"./a"})
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", race), goInvocation("plain", plain)})
	ctx := context.Background()
	const example = "example.com/units/a.ExampleA"
	_, fullRun, err := ExecutePolicyWitnessed(ctx, mustCapture(t, ctx, writeModule(t, files), pol), noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if fullRun.Ran != 1 || fullRun.Uncached != 0 || fullRun.Outcomes[example] != verify.TestPassed {
		t.Fatalf("full form: ran %d, uncached %d, example outcome %v (reasons %v); want the example executed and counted in neither", fullRun.Ran, fullRun.Uncached, fullRun.Outcomes[example], fullRun.UncacheableReasons)
	}
	// The selective form over a cold module: the witness executes under
	// the race leg, the example under the plain one — whose pass
	// outcomes the run strips by contract (a plain leg indicts, never
	// grants), so the example's row reaches the executed count alone;
	// the same count.
	serving, err := RunWitnessesScoped(ctx, mustCapture(t, ctx, writeModule(t, files), pol), nil, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if serving.Ran != 1 || serving.Uncached != 0 {
		t.Fatalf("selective form: ran %d, uncached %d (reasons %v); want the example counted in neither", serving.Ran, serving.Uncached, serving.UncacheableReasons)
	}
}

// TestUngrantedEligibleWitnessIsNotOutsideTheSelection pins the
// selection/execution distinction on the health-judged form: the
// witnesses of a package an eligible race invocation selects, whose
// process an early test holds past the invocation's envelope, are not
// outside the selection — the one that ran and passed, the one that
// hung, the one never reached, and a race-tag-gated one the universe
// never lists (-race implies the race build tag, so the eligible leg's
// discovery lists it and the default universe does not) all carry the
// package's timeout under that invocation as their cause, and every
// timed-out package keeps its retained diagnostic on the report; a
// passing test beside a failing sibling and a subject behind an
// exiting TestMain carry their packages' dispositions — while a
// subject no eligible invocation selects stays outside
// (REQ-check-witness-selection).
//
// Deliberately not //gofresh:pure: executes the fixture's tests.
func TestUngrantedEligibleWitnessIsNotOutsideTheSelection(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	if testing.Short() {
		t.Skip("executes a race invocation over a temporary module until its envelope expires")
	}
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tmp := writeModule(t, map[string]string{
		"go.mod":      "module example.com/units\n\ngo 1.26\n",
		"a/a_test.go": "package a\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestEarly(t *testing.T) {}\n\nfunc TestBlocks(t *testing.T) { time.Sleep(30 * time.Second) }\n\nfunc TestLater(t *testing.T) {}\n",
		"a/z_test.go": "//go:build race\n\npackage a\n\nimport \"testing\"\n\nfunc TestRaceOnly(t *testing.T) {}\n",
		"c/c_test.go": "package c\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestHolds(t *testing.T) { time.Sleep(30 * time.Second) }\n",
		"d/d_test.go": "package d\n\nimport \"testing\"\n\nfunc TestFails(t *testing.T) { t.Fatal(\"red\") }\n\nfunc TestBeside(t *testing.T) {}\n",
		"f/f_test.go": "package f\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(0) }\n\nfunc TestNever(t *testing.T) {}\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestPlain(t *testing.T) {}\n",
	})
	race := &stipulatorv1.GoInvocationConfig{}
	race.SetPackages([]string{"./a", "./c"})
	race.SetRace(true)
	// The failing and the row-less packages run under their own eligible
	// invocation with a generous envelope: their arms are about a
	// package's disposition, never about racing the timeout above.
	raceD := &stipulatorv1.GoInvocationConfig{}
	raceD.SetPackages([]string{"./d", "./f"})
	raceD.SetRace(true)
	plain := &stipulatorv1.GoInvocationConfig{}
	plain.SetPackages([]string{"./b"})
	raceInv := goInvocation("race", race)
	raceInv.SetTimeout(durationpb.New(8 * time.Second))
	raceDInv := goInvocation("race-d", raceD)
	raceDInv.SetTimeout(durationpb.New(3 * time.Minute))
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{raceInv, raceDInv, goInvocation("plain", plain)})
	ctx := context.Background()
	report, tr, err := ExecutePolicyWitnessed(ctx, mustCapture(t, ctx, tmp, pol), noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	timedOut := map[string]bool{}
	for _, h := range report.GetInvocations() {
		for _, p := range h.GetPackages() {
			if h.GetInvocation() == "race" && p.GetDisposition() == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
				timedOut[p.GetPackage()] = true
			}
		}
	}
	if !timedOut["example.com/units/a"] || !timedOut["example.com/units/c"] {
		t.Fatalf("the fixture's race invocation did not time out both packages: %+v", report.GetInvocations())
	}
	// Every timed-out package keeps its retained diagnostic on the report.
	diagnosed := map[string]bool{}
	for _, d := range report.GetDiagnostics() {
		diagnosed[d.GetPackage()] = true
	}
	if !diagnosed["example.com/units/a"] || !diagnosed["example.com/units/c"] {
		t.Fatalf("timed-out packages without a retained diagnostic: %v", diagnosed)
	}
	for _, key := range []string{"example.com/units/a.TestEarly", "example.com/units/a.TestBlocks", "example.com/units/a.TestLater", "example.com/units/a.TestRaceOnly", "example.com/units/c.TestHolds"} {
		if tr.OutsideSubjects[key] {
			t.Fatalf("an eligible-covered subject classed outside: %s", key)
		}
	}
	if !tr.OutsideSubjects["example.com/units/b.TestPlain"] || tr.OutsidePolicy != 1 {
		t.Fatalf("outside = %v (%d); want b.TestPlain alone", tr.OutsideSubjects, tr.OutsidePolicy)
	}
	for key, pkg := range map[string]string{"example.com/units/a.TestEarly": "a", "example.com/units/a.TestBlocks": "a", "example.com/units/a.TestLater": "a", "example.com/units/a.TestRaceOnly": "a", "example.com/units/c.TestHolds": "c"} {
		want := "invocation race: package example.com/units/" + pkg + " timeout"
		if got := tr.NoOutcome[key]; got != want {
			t.Fatalf("%s: no-outcome cause = %q, want %q", key, got, want)
		}
		if _, ok := tr.Outcomes[key]; ok {
			t.Fatalf("%s was granted an outcome under a timed-out process", key)
		}
	}
	if _, ok := tr.NoOutcome["example.com/units/b.TestPlain"]; ok {
		t.Fatal("an outside subject also carried a no-outcome cause")
	}
	// A passing test beside a failing sibling ran — its row is on the
	// report — and was granted nothing: the failing package is its
	// cause; the failure itself is a fact with its own outcome. The
	// package's disposition is the arm's premise, asserted first.
	health := reportPackageHealth(report)
	if got := health["race-d\x00example.com/units/d"]; got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("package d disposed %v under race-d, want test failed", got)
	}
	if out, ok := tr.Outcomes["example.com/units/d.TestFails"]; !ok || out != verify.TestFailed {
		t.Fatalf("d.TestFails outcome = %v (%v), want the failure recorded", out, ok)
	}
	if _, ok := tr.Outcomes["example.com/units/d.TestBeside"]; ok {
		t.Fatal("a pass beside a failing sibling was granted an outcome")
	}
	if got := tr.NoOutcome["example.com/units/d.TestBeside"]; got != "invocation race-d: package example.com/units/d test failed" {
		t.Fatalf("d.TestBeside no-outcome cause = %q", got)
	}
	if !executedTopKeys(report)["example.com/units/d.TestBeside"] {
		t.Fatal("d.TestBeside left no row on the report; the arm discriminates nothing")
	}
	// A TestMain that exits before m.Run: the process disposes healthy
	// and rows nothing, so the discovered subject's cause names the
	// healthy package that reported no result for it.
	if got := health["race-d\x00example.com/units/f"]; got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("package f disposed %v under race-d, want healthy", got)
	}
	if got := tr.NoOutcome["example.com/units/f.TestNever"]; got != "invocation race-d: package example.com/units/f healthy, no result for it" {
		t.Fatalf("f.TestNever no-outcome cause = %q", got)
	}
}

// TestUngrantedEligibleWitnessCarriesItsCauseOnTheSelectiveForm pins
// the same distinction on the serving form: a package's process times
// out and the run names the timeout under its invocation as the cause
// of every subject it granted nothing — the one that ran, the one that
// hung, the one never reached — outside only what no eligible
// invocation selects (REQ-check-witness-selection).
//
// Deliberately not //gofresh:pure: executes the fixture's tests.
func TestUngrantedEligibleWitnessCarriesItsCauseOnTheSelectiveForm(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	if testing.Short() {
		t.Skip("executes a race invocation over a temporary module until its envelope expires")
	}
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tmp := writeModule(t, map[string]string{
		"go.mod":      "module example.com/units\n\ngo 1.26\n",
		"a/a_test.go": "package a\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestEarly(t *testing.T) {}\n\nfunc TestBlocks(t *testing.T) { time.Sleep(30 * time.Second) }\n\nfunc TestLater(t *testing.T) {}\n",
		"e/e_test.go": "package e\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestQueued(t *testing.T) { time.Sleep(30 * time.Second) }\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestPlain(t *testing.T) {}\n",
	})
	race := &stipulatorv1.GoInvocationConfig{}
	race.SetPackages([]string{"./a", "./e"})
	race.SetRace(true)
	plain := &stipulatorv1.GoInvocationConfig{}
	plain.SetPackages([]string{"./b"})
	raceInv := goInvocation("race", race)
	raceInv.SetTimeout(durationpb.New(8 * time.Second))
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{raceInv, goInvocation("plain", plain)})
	ctx := context.Background()
	pc := mustCapture(t, ctx, tmp, pol)
	// One package spawns at a time: the second waits behind the first's
	// sleep until the envelope expires and never spawns — a process the
	// envelope denied, disposed timeout with no producer.
	for _, n := range pc.normalized {
		n.SpawnBound = 1
	}
	tr, err := RunWitnessesPolicy(ctx, pc, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if tr.OutsideSubjects["example.com/units/a.TestLater"] || !tr.OutsideSubjects["example.com/units/b.TestPlain"] || tr.OutsidePolicy != 1 {
		t.Fatalf("outside = %v (%d); want b.TestPlain alone", tr.OutsideSubjects, tr.OutsidePolicy)
	}
	for key, pkg := range map[string]string{"example.com/units/a.TestEarly": "a", "example.com/units/a.TestBlocks": "a", "example.com/units/a.TestLater": "a", "example.com/units/e.TestQueued": "e"} {
		want := "invocation race: package example.com/units/" + pkg + " timeout"
		if got := tr.NoOutcome[key]; got != want {
			t.Fatalf("%s: no-outcome cause = %q, want %q (outcomes %v)", key, got, want, tr.Outcomes)
		}
	}
	// A caller's scope leaves the out-of-scope subjects unexecuted by
	// the caller, not by the execution: no cause for them, the cause
	// for the in-scope one the blocker held.
	scoped, err := RunWitnessesScoped(ctx, mustCapture(t, ctx, tmp, pol), map[gofresh.Subject]bool{
		{Package: "example.com/units/a", Symbol: "TestBlocks"}: true,
		{Package: "example.com/units/a", Symbol: "TestLater"}:  true,
	}, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if got := scoped.NoOutcome["example.com/units/a.TestLater"]; got != "invocation race: package example.com/units/a timeout" {
		t.Fatalf("scoped in-scope cause = %q (no-outcome %v)", got, scoped.NoOutcome)
	}
	if _, ok := scoped.NoOutcome["example.com/units/a.TestEarly"]; ok || !scoped.ScopeSkipped["example.com/units/a.TestEarly"] {
		t.Fatalf("a scope-skipped subject carried a cause or lost its skip: %v / %v", scoped.NoOutcome, scoped.ScopeSkipped)
	}
}
