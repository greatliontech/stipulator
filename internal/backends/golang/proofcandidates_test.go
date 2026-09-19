package golang

import (
	"slices"
	"testing"

	gofresh "github.com/greatliontech/gofresh"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestProofCandidatesTakeSoloProcessesWithoutAssertions pins the one
// candidate derivation the selective run and the drift retry share: a
// subject is a proof candidate exactly when it is alone in its package
// among the execution's subjects, captured, and carries no author's
// purity assertion — in the subjects' order.
func TestProofCandidatesTakeSoloProcessesWithoutAssertions(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	sub := func(pkg, sym string) gofresh.Subject { return gofresh.Subject{Package: pkg, Symbol: sym} }
	subjects := []gofresh.Subject{
		sub("z", "TestSolo"),
		sub("pair", "TestFirst"),
		sub("pair", "TestSecond"),
		sub("asserted", "TestPure"),
		sub("uncaptured", "TestLost"),
		sub("a", "TestAlone"),
	}
	fps := map[gofresh.Subject]gofresh.Fingerprint{
		sub("z", "TestSolo"):        {},
		sub("pair", "TestFirst"):    {},
		sub("pair", "TestSecond"):   {},
		sub("asserted", "TestPure"): {PurityAssertion: "author"},
		sub("a", "TestAlone"):       {},
	}
	got := proofCandidates(subjects, fps)
	want := []gofresh.Subject{sub("z", "TestSolo"), sub("a", "TestAlone")}
	if !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want the solo unasserted captured subjects in the subjects' order %v", got, want)
	}
	if got := proofCandidates(nil, fps); len(got) != 0 {
		t.Fatalf("candidates over no subjects = %v, want none", got)
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
