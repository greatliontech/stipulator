package golang

import (
	"maps"
	"reflect"
	"strconv"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/stipulate"
	"pgregory.net/rapid"
)

func TestWitnessAllocation(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-concurrency")
	for _, tc := range []struct {
		processors, packages int
		env                  string
		bound, width         int
	}{
		{24, 0, "", 1, 24}, {24, 1, "", 1, 24}, {24, 2, "", 2, 12},
		{24, 100, "", 12, 2}, {7, 2, "", 2, 3}, {7, 100, "", 3, 2},
		{1, 0, "", 1, 1}, {1, 100, "", 1, 1}, {2, 100, "", 1, 2},
		{3, 100, "", 1, 3}, {24, 1, "GOMAXPROCS=3", 1, 3},
		{24, 2, "GOMAXPROCS=24", 2, 12}, {24, 1, "GOMAXPROCS=invalid", 1, 24},
	} {
		n := &NormalizedInvocation{processorBudget: tc.processors, Env: []string{"KEEP=unchanged"}}
		if tc.env != "" {
			n.Env = append(n.Env, tc.env)
		}
		original := append([]string(nil), n.Env...)
		freezeWitnessAllocation(n, tc.packages)
		width, _ := lookupEnv(n.WitnessEnv, "GOMAXPROCS")
		if n.SpawnBound != tc.bound || width != strconv.Itoa(tc.width) {
			t.Fatalf("%+v: bound=%d width=%s", tc, n.SpawnBound, width)
		}
		if !reflect.DeepEqual(n.Env, original) {
			t.Fatal("allocation mutated the normalized input environment")
		}
		frozen := append([]string(nil), n.WitnessEnv...)
		freezeWitnessAllocation(n, 1)
		if n.SpawnBound != tc.bound || !reflect.DeepEqual(n.WitnessEnv, frozen) {
			t.Fatal("retry changed the frozen allocation")
		}
	}
}

func TestWitnessAllocationBudgetProperty(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-concurrency")
	rapid.Check(t, func(t *rapid.T) {
		processors := rapid.IntRange(1, 512).Draw(t, "processors")
		packages := rapid.IntRange(0, 1024).Draw(t, "packages")
		declared := rapid.IntRange(1, 1024).Draw(t, "environment-width")
		n := &NormalizedInvocation{processorBudget: processors, Env: []string{"GOMAXPROCS=" + strconv.Itoa(declared)}}
		freezeWitnessAllocation(n, packages)
		value, _ := lookupEnv(n.WitnessEnv, "GOMAXPROCS")
		width, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
		bound := n.SpawnBound
		if bound < 1 || bound > max(1, packages) || bound > max(1, processors/2) || bound*width > processors || width < 1 || width > declared {
			t.Fatalf("budget exceeded: P=%d K=%d env=%d B=%d W=%d", processors, packages, declared, bound, width)
		}
		if bound < max(1, packages) && bound < max(1, processors/2) {
			t.Fatal("package allocation left an admissible slot unused")
		}
		if width < declared && bound*(width+1) <= processors {
			t.Fatal("child allocation left an admissible processor unused")
		}
	})
}

func TestExecutionScopePackagePopulation(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-concurrency", "REQ-check-verdict-scoped")
	obs := []Obligation{
		{Kind: ObligationPackage, Package: "empty"},
		{Kind: ObligationPackage, Package: "examples"}, {Kind: ObligationExample, Package: "examples", Name: "Example"},
		{Kind: ObligationPackage, Package: "shared"}, {Kind: ObligationTest, Package: "shared", Name: "TestA"},
		{Kind: ObligationTest, Package: "shared", Name: "TestB"},
		{Kind: ObligationPackage, Package: "sole"}, {Kind: ObligationFuzz, Package: "sole", Name: "FuzzA"},
	}
	counts := map[string]int{"empty": 1, "examples": 1, "shared": 2, "sole": 1}
	for _, tc := range []struct {
		name           string
		eligible, full bool
		scope          map[gofresh.Subject]bool
		want           int
	}{
		{name: "full includes empty and examples", full: true, want: 4},
		{name: "eligible including ambiguous", eligible: true, want: 2},
		{name: "ineligible shared only", want: 1},
		{name: "scoped ambiguous", eligible: true, scope: map[gofresh.Subject]bool{{Package: "shared", Symbol: "TestA"}: true}, want: 1},
		{name: "scoped ineligible", scope: map[gofresh.Subject]bool{{Package: "shared", Symbol: "TestA"}: true}, want: 1},
		{name: "ineligible sole", scope: map[gofresh.Subject]bool{{Package: "sole", Symbol: "FuzzA"}: true}, want: 0},
		{name: "unknown name in known package", eligible: true, scope: map[gofresh.Subject]bool{{Package: "shared", Symbol: "TestUnknown"}: true}, want: 0},
		{name: "empty scope", eligible: true, scope: map[gofresh.Subject]bool{}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ic := invocationCapture{n: &NormalizedInvocation{Race: tc.eligible}, obligations: obs}
			selection := executionScope{full: tc.full, subjects: tc.scope}
			if got := selection.packageCount(ic, counts); got != tc.want {
				t.Fatalf("packages=%d, want %d", got, tc.want)
			}
		})
	}
}

func TestExecutionScopeOwnsSelection(t *testing.T) {
	in := gofresh.Subject{Package: "p", Symbol: "TestIn"}
	out := gofresh.Subject{Package: "q", Symbol: "TestOut"}
	scope := map[gofresh.Subject]bool{in: true}
	pc := &Capture{}
	if err := pc.ConfigureExecution(false, scope); err != nil {
		t.Fatal(err)
	}
	scope[out] = true
	if pc.execution.permits(out) {
		t.Fatal("capture borrowed its caller's mutable scope")
	}
	pc.discovered.done = true
	if err := pc.ConfigureExecution(false, map[gofresh.Subject]bool{in: true}); err != nil {
		t.Fatal(err)
	}
	if err := pc.ConfigureExecution(false, scope); err == nil {
		t.Fatal("accepted a scope change after discovery")
	}
	if err := pc.ConfigureExecution(true, nil); err == nil {
		t.Fatal("accepted a class change after discovery")
	}
	if err := (&Capture{}).ConfigureExecution(true, map[gofresh.Subject]bool{}); err == nil {
		t.Fatal("accepted full execution with a scope")
	}
	skipped := map[string]bool{}
	for range 2 {
		got := pc.execution.retrySubjects([]gofresh.Subject{out, in}, skipped)
		if !reflect.DeepEqual(got, []gofresh.Subject{in}) || !maps.Equal(skipped, map[string]bool{"q.TestOut": true}) {
			t.Fatalf("retry selection=%v skipped=%v", got, skipped)
		}
	}
}
