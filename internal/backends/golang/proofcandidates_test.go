package golang

import (
	"slices"
	"testing"

	gofresh "github.com/greatliontech/gofresh"

	"github.com/greatliontech/stipulator/stipulate"
)

// Preparation covers every actual runnable, independently of which witnesses
// can publish. Examples and never-serving tests can affect a sibling's proof.
func TestNativePreparationIncludesTheWholeRunnableRoster(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-purity")
	sub := func(pkg, sym string) gofresh.Subject { return gofresh.Subject{Package: pkg, Symbol: sym} }
	g := &runnableGeneration{packages: map[string]runnablePackage{
		"pair": {runnables: []Obligation{{Kind: ObligationTest, Name: "TestSecond"}, {Kind: ObligationExample, Name: "Example"}, {Kind: ObligationTest, Name: "TestFirst"}}},
		"z":    {runnables: []Obligation{{Kind: ObligationTest, Name: "TestSolo"}}},
	}}
	got := g.subjects()
	want := []gofresh.Subject{sub("pair", "Example"), sub("pair", "TestFirst"), sub("pair", "TestSecond"), sub("z", "TestSolo")}
	if !slices.Equal(got, want) {
		t.Fatalf("actual runnable preparation = %v, want %v", got, want)
	}
	if got := (&runnableGeneration{}).subjects(); len(got) != 0 {
		t.Fatalf("empty package roster invented subjects: %v", got)
	}
}

// TestSelectingInvocationAnswersNothingForAnAbsentPackage pins the
// group's package lookup a cross-group walk relies on: a package the
// group does not hold names no invocation, never a fault.
func TestSelectingInvocationAnswersNothingForAnAbsentPackage(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	g := &captureGroup{packages: map[string]*groupPackage{"held": {inv: "race"}}}
	if got := g.selectingInvocation("held"); got != "race" {
		t.Fatalf("held package's invocation = %q, want race", got)
	}
	if got := g.selectingInvocation("absent"); got != "" {
		t.Fatalf("absent package's invocation = %q, want none", got)
	}
}

// TestSubjectsOfOrdersByPackageThenSymbol pins the order every
// selection-derived subject list takes: by package, then symbol,
// whatever order the selection's names arrived in.
func TestSubjectsOfOrdersByPackageThenSymbol(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	got := subjectsOf(map[string][]string{
		"b": {"TestZ", "TestA"},
		"a": {"TestM", "TestB"},
	})
	want := []gofresh.Subject{
		{Package: "a", Symbol: "TestB"}, {Package: "a", Symbol: "TestM"},
		{Package: "b", Symbol: "TestA"}, {Package: "b", Symbol: "TestZ"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("subjects = %v, want %v", got, want)
	}
	if got := subjectsOf(nil); len(got) != 0 {
		t.Fatalf("subjects of an empty selection = %v, want none", got)
	}
}
