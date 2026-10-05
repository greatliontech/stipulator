package golang

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/stipulate"
)

// A quiesced serving backend publishes its records and closes its child,
// then answers every question it has already heard — resolution, package,
// class, serving refusal — from memory, spawning no second child; a
// symbol it never heard costs a child again
// (REQ-evidence-resolution-freshness).
func TestQuiescedBackendAnswersFromMemory(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("loads a fixture module's types and views")
	}
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":                       "module example.com/quiet\n\ngo 1.26\n",
		"p/p.go":                       "package p\n\nfunc F(n int) int { return n + 1 }\n\nfunc G(n int) int { return n + 2 }\n",
		"p/p_test.go":                  "package p\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(1) != 2 {\n\t\tt.Fatal(\"F\")\n\t}\n}\n",
		".stipulator/policy.textproto": "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
	})
	c := countSpawns(t)
	ctx := context.Background()
	s, err := NewServed(ctx, dir, []string{"example.com/quiet/p.F", "example.com/quiet/p.TestF", "example.com/quiet/p.G"})
	if err != nil {
		t.Fatal(err)
	}
	before := ask(t, s, "example.com/quiet/p.F")
	beforeTest := ask(t, s, "example.com/quiet/p.TestF")
	if c.snapshot().child != 1 {
		t.Fatalf("spawns before quiesce = %+v; want one child", c.snapshot())
	}
	s.Quiesce()
	if s.child != nil {
		t.Fatal("quiesce left the child open")
	}
	if n := len(resolutioncache.Load(dir)); n != 2 {
		t.Fatalf("quiesce published %d records; want the two asked symbols", n)
	}
	if !strings.Contains(strings.Join(s.Notices(), "\n"), "resolution published under \"default\": 2 records") {
		t.Fatalf("the account after the quiesce names %v; want 2 records", s.Notices())
	}
	// Every heard question answers from memory: no second child.
	if after := ask(t, s, "example.com/quiet/p.F"); after != before {
		t.Fatalf("F after quiesce = %+v; before %+v", after, before)
	}
	if after := ask(t, s, "example.com/quiet/p.TestF"); after != beforeTest {
		t.Fatalf("TestF after quiesce = %+v; before %+v", after, beforeTest)
	}
	// The seeding classification answers from the memo too: the
	// non-test symbol's recorded refusal, the test's recorded absence.
	if refusals, err := s.NeverServe([]string{"example.com/quiet/p.F", "example.com/quiet/p.TestF"}); err != nil || len(refusals) != 1 || refusals["example.com/quiet/p.F"] == "" {
		t.Fatalf("seeding after quiesce = %v, %v; want F's recorded refusal alone", refusals, err)
	}
	if got := c.snapshot().child; got != 1 {
		t.Fatalf("a heard question spawned a child again: %d spawns", got)
	}
	// A question never heard costs a child again — correct, and
	// accounted by the spawn seam.
	ask(t, s, "example.com/quiet/p.G")
	if got := c.snapshot().child; got != 2 {
		t.Fatalf("an unheard question spawned %d children in all; want a second", got)
	}
	// The close owes nothing: the records published at the quiesce stand
	// alone, plus G's.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := len(resolutioncache.Load(dir)); n != 3 {
		t.Fatalf("records after close = %d; want the three asked symbols", n)
	}
	// The account sums the quiesce's publish and the close's: three
	// records, never the last publish's one.
	if !strings.Contains(strings.Join(s.Notices(), "\n"), "resolution published under \"default\": 3 records") {
		t.Fatalf("the account after the close names %v; want 3 records in all", s.Notices())
	}
}

// The publish account under one key is the sum of its publishes — a
// quiesce's and a later close's after an unheard question respawned
// the child: every count added, the refused lists joined in publish
// order (REQ-evidence-resolution-cache-format's never-silent account).
//
//gofresh:pure
func TestPublishAccountSumsAcrossPublishes(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	// The prior's refused list carries spare capacity: an append that
	// aliased it would write the later refusals into the slot past its
	// length, which the assertion below reads.
	priorRefused := make([]string, 1, 4)
	priorRefused[0] = "a: tier"
	prior := publishAccount{installed: 2, moved: 1, unopened: 3, uncaptured: 4, refused: priorRefused}
	later := publishAccount{installed: 1, moved: 5, unopened: 6, uncaptured: 7, refused: []string{"b: tier", "c: tier"}}
	got := later.summed(prior)
	want := publishAccount{installed: 3, moved: 6, unopened: 9, uncaptured: 11, refused: []string{"a: tier", "b: tier", "c: tier"}}
	if got.installed != want.installed || got.moved != want.moved || got.unopened != want.unopened || got.uncaptured != want.uncaptured || !slices.Equal(got.refused, want.refused) {
		t.Fatalf("summed = %+v; want %+v", got, want)
	}
	if first := later.summed(publishAccount{}); first.installed != 1 || first.moved != 5 || first.unopened != 6 || first.uncaptured != 7 || !slices.Equal(first.refused, later.refused) {
		t.Fatalf("a first publish summed with nothing = %+v; want itself", first)
	}
	if spare := priorRefused[:cap(priorRefused)][1]; spare != "" {
		t.Fatalf("summing aliased the prior's refused list: its spare slot holds %q", spare)
	}
}

// orderedSeeding is a seeding that records its classification and when
// the witness run released it, against the run's spawns and phases.
type orderedSeeding struct {
	record func(string)
}

func (o orderedSeeding) NeverServe([]string) (map[string]string, error) {
	o.record("classify")
	return map[string]string{}, nil
}

func (o orderedSeeding) Quiesce() { o.record("quiesce") }

// Both witness forms release their seeding backend right after its
// classification — before the run's listings, its engines' loads and
// its first process spawn, and so before the execution phase — so the
// backend's cost is never resident through the run
// (REQ-evidence-resolution-freshness).
func TestWitnessRunsQuiesceTheSeedingBeforeExecuting(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("executes a race invocation over a temporary module")
	}
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tmp := writeModule(t, map[string]string{
		"go.mod":      "module example.com/units\n\ngo 1.26\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
	})
	all := &stipulatorv1.GoInvocationConfig{}
	all.SetPackages([]string{"./a"})
	all.SetRace(true)
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", all)})
	for _, form := range []string{"selective", "health-judged"} {
		var mu sync.Mutex
		var events []string
		record := func(e string) {
			mu.Lock()
			defer mu.Unlock()
			if len(events) == 0 || events[len(events)-1] != e || e == "classify" || e == "quiesce" {
				events = append(events, e)
			}
		}
		rep := progress.New(func(e *stipulatorv1.ProgressEvent) {
			if e.GetPhase() == stipulatorv1.Phase_PHASE_EXECUTION {
				record("execution")
			}
		}, progress.WithInterval(time.Hour))
		ctx := progress.NewContext(context.Background(), rep)
		// The capture precedes the run and spawns its own listings; the
		// spawn seam installs after it, so every spawn recorded is the
		// run's.
		pc := mustCapture(t, ctx, tmp, pol)
		prior := commandHook
		commandHook = func(string, []string) { record("spawn") }
		t.Cleanup(func() { commandHook = prior })
		beforeGroupEngineForTest = func() { record("engine") }
		t.Cleanup(func() { beforeGroupEngineForTest = nil })
		seeding := orderedSeeding{record: record}
		var err error
		if form == "selective" {
			_, err = RunWitnessesPolicy(ctx, pc, seeding)
		} else {
			_, _, err = ExecutePolicyWitnessed(ctx, pc, seeding)
		}
		commandHook = prior
		beforeGroupEngineForTest = nil
		if err != nil {
			t.Fatalf("%s: %v", form, err)
		}
		classify, quiesce, engine, execution := slices.Index(events, "classify"), slices.Index(events, "quiesce"), slices.Index(events, "engine"), slices.Index(events, "execution")
		// The capture's discovery spawns before the classification; the
		// rule binds what follows it: the release is the very next
		// event — no listing, load or spawn between — before the first
		// engine's construction and the execution phase.
		if classify < 0 || quiesce != classify+1 || engine < quiesce || execution < quiesce {
			t.Fatalf("%s: order %v; want classify, then quiesce with no spawn between, before the first engine and the execution phase", form, events)
		}
	}
}
