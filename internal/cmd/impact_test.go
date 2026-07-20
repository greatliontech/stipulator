package cmd

import (
	"bytes"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/diff"
	"github.com/greatliontech/stipulator/internal/impact"
	"github.com/greatliontech/stipulator/stipulate"
)

// The rendering names every candidate class and closes with the advisory
// posture: candidates, never verdicts — the words "stale" or "fresh"
// would claim what only the witnessed surfaces can decide.
//
//gofresh:pure
func TestImpactRenderNamesCandidatesAndAdvisoryFooter(t *testing.T) {
	stipulate.Covers(t, "REQ-change-impact")
	r := &impact.Report{
		Changed: []string{"leaf/leaf.go"},
		Spec:    &diff.Report{TextChangedRequirements: []string{"REQ-x"}},
		Bound: []impact.BoundHit{{
			Requirement: "REQ-x",
			Symbol:      "example.com/m/leaf.Double",
			File:        "leaf/leaf.go",
			Role:        stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS,
		}},
		Witnesses: []impact.WitnessHit{{
			Requirement: "REQ-x",
			Symbol:      "example.com/m/mid.TestQuad",
		}},
	}
	var out bytes.Buffer
	renderImpact(&out, r)
	got := out.String()
	for _, want := range []string{
		"changed: 1 file against HEAD",
		"spec: text-changed REQ-x",
		"bound: REQ-x  implements  example.com/m/leaf.Double  (leaf/leaf.go)",
		"witness reached: REQ-x  example.com/m/mid.TestQuad",
		"preview only",
		"advisory, not proof",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendering misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "stale") || strings.Contains(got, "fresh") {
		t.Errorf("rendering claims a freshness verdict:\n%s", got)
	}
}

// Quiescence needs every section empty: a spec delta with an empty
// change set (a gitignored spec document) still renders, and only a
// fully empty preview reads as "no changes".
//
//gofresh:pure
func TestImpactRenderEmptyChangeSetStillShowsSpecDelta(t *testing.T) {
	stipulate.Covers(t, "REQ-change-impact")
	var out bytes.Buffer
	renderImpact(&out, &impact.Report{
		Spec: &diff.Report{AddedRequirements: []string{"REQ-ghost"}},
	})
	if !strings.Contains(out.String(), "spec: added REQ-ghost") {
		t.Errorf("spec delta hidden behind an empty change set:\n%s", out.String())
	}

	out.Reset()
	renderImpact(&out, &impact.Report{Spec: &diff.Report{}})
	if got := out.String(); got != "no changes against HEAD\n" {
		t.Errorf("quiescent preview = %q", got)
	}
}
