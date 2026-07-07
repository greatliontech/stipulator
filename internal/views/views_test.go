package views

import (
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/harden"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestHardenViews pins the harden view axis: the summary carries counts
// and only the OPEN survivors — attested ones and their prose stay in
// the full view.
func TestHardenViews(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-views")
	rep := &harden.Report{Results: []harden.Result{{
		Symbol:  "example.com/p.F",
		Mutants: 5,
		Killed:  3,
		Survivors: []harden.Survivor{
			{Position: "f.go:5:2", Operator: "zero return"},
			{Position: "f.go:9:1", Operator: "drop assignment"},
		},
		Attested: []harden.Attestation{{Position: "f.go:9:1", Operator: "drop assignment", Reason: "equivalent: derived below"}},
	}}}

	m, err := HardenView(rep, "summary")
	if err != nil {
		t.Fatal(err)
	}
	sum := m.(*stipulatorv1.HardenSummary)
	res := sum.GetResults()[0]
	if res.GetMutants() != 5 || res.GetKilled() != 3 || res.GetAttested() != 1 {
		t.Fatalf("summary counts: %v", res)
	}
	if len(res.GetOpenSurvivors()) != 1 || res.GetOpenSurvivors()[0].GetPosition() != "f.go:5:2" {
		t.Fatalf("open survivors wrong: %v", res.GetOpenSurvivors())
	}
	if strings.Contains(res.String(), "equivalent: derived below") {
		t.Fatal("attestation prose leaked into the summary")
	}

	full, err := HardenView(rep, "full")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full.(*stipulatorv1.HardenReport).String(), "equivalent: derived below") {
		t.Fatal("full view lost the attestation prose")
	}

	if _, err := HardenView(rep, "everything"); err == nil {
		t.Fatal("unknown view accepted")
	}
}

// TestVerifyViews pins the verify view axis: the summary is counts and
// signatures with no binding rows; the bindings view scopes.
func TestVerifyViews(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-views")
	vr := &verify.Report{
		Results: []verify.BindingResult{
			{RequirementId: "REQ-v-a", Symbol: "example.com/p.TestA", Role: stipulatorv1.BindingRole_BINDING_ROLE_TESTS},
			{RequirementId: "REQ-v-b", Symbol: "example.com/q.TestB", Role: stipulatorv1.BindingRole_BINDING_ROLE_TESTS},
		},
		TestsPassed: 2,
		Signatures:  []verify.ChangeSignature{{RequirementId: "REQ-v-a", Label: verify.SemanticDrift, Evidence: []string{"e"}}},
	}
	facts := Facts{Doc: map[string]string{"REQ-v-a": "specs/a.md", "REQ-v-b": "specs/b.md"}, Symbols: map[string][]string{}}

	m, err := VerifyView(vr, facts, "", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	sum := m.(*stipulatorv1.VerifySummary)
	if sum.GetTestsPassed() != 2 || len(sum.GetSignatures()) != 1 {
		t.Fatalf("summary: %v", sum)
	}

	m, err = VerifyView(vr, facts, "bindings", Scope{Path: "example.com/q"})
	if err != nil {
		t.Fatal(err)
	}
	rows := m.(*stipulatorv1.VerifyReport).GetResults()
	if len(rows) != 1 || rows[0].GetSymbol() != "example.com/q.TestB" {
		t.Fatalf("scoped bindings: %v", rows)
	}

	// Bucket scope has no meaning over binding rows: refused, never
	// silently empty.
	if _, err := VerifyView(vr, facts, "bindings", Scope{Bucket: "covered"}); err == nil {
		t.Fatal("bucket scope over bindings accepted")
	}
}

// TestCoverageViewScopesGapsAndViolations pins that a scope narrows the
// WHOLE report, not just the requirement rows: the reds and full views must
// filter gaps and violations to the scope, so filtered triage is not
// polluted by out-of-scope entries — while the gate verdict stays global.
func TestCoverageViewScopesGapsAndViolations(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-views")
	cov := &coverage.Report{
		Requirements: []coverage.Requirement{
			{Id: "REQ-corpus-a", Bucket: coverage.Uncovered}, // gapped
			{Id: "REQ-corpus-b", Bucket: coverage.Uncovered}, // violation
			{Id: "REQ-arch-x", Bucket: coverage.Uncovered},   // gapped, out of scope
			{Id: "REQ-arch-y", Bucket: coverage.Uncovered},   // violation, out of scope
		},
		Gaps: []coverage.Gap{
			{RequirementId: "REQ-corpus-a", State: coverage.Open},
			{RequirementId: "REQ-arch-x", State: coverage.Open},
			// An orphan gap: its requirement is absent from Requirements, so
			// it must survive the UNSCOPED view rather than be dropped.
			{RequirementId: "REQ-gone", State: coverage.Open},
		},
		Violations: []string{"REQ-arch-y", "REQ-corpus-b"},
	}
	facts := Facts{Doc: map[string]string{}, Symbols: map[string][]string{}}

	for _, view := range []string{"reds", "full"} {
		m, err := CoverageView(cov, facts, view, Scope{Filter: "REQ-corpus-*"})
		if err != nil {
			t.Fatalf("%s: %v", view, err)
		}
		rep := m.(*stipulatorv1.CoverageReport)
		for _, g := range rep.GetGaps() {
			if !strings.HasPrefix(g.GetRequirementId(), "REQ-corpus-") {
				t.Fatalf("%s view leaked out-of-scope gap %q", view, g.GetRequirementId())
			}
		}
		if len(rep.GetGaps()) != 1 {
			t.Fatalf("%s: want the one in-scope gap, got %v", view, rep.GetGaps())
		}
		for _, v := range rep.GetViolations() {
			if !strings.HasPrefix(v, "REQ-corpus-") {
				t.Fatalf("%s view leaked out-of-scope violation %q", view, v)
			}
		}
		if len(rep.GetViolations()) != 1 {
			t.Fatalf("%s: want the one in-scope violation, got %v", view, rep.GetViolations())
		}
		// The verdict is global: the tree fails on out-of-scope violations,
		// so a scoped slice must not report a passing gate.
		if rep.GetGatePasses() {
			t.Fatalf("%s view reported a passing gate for a failing tree", view)
		}
	}

	// Global verdict under a scope that contains NO in-scope violation:
	// REQ-corpus-a is gapped, not a violation, so the scoped slice's
	// violations are empty — yet the tree fails on out-of-scope violations,
	// so the reported gate must still be false. This is what distinguishes
	// the global verdict from the slice's own.
	m0, err := CoverageView(cov, facts, "full", Scope{Ids: []string{"REQ-corpus-a"}})
	if err != nil {
		t.Fatal(err)
	}
	rep0 := m0.(*stipulatorv1.CoverageReport)
	if len(rep0.GetViolations()) != 0 {
		t.Fatalf("scope with no in-scope violation still listed some: %v", rep0.GetViolations())
	}
	if rep0.GetGatePasses() {
		t.Fatal("empty scoped violations reported a passing gate for a failing tree")
	}

	// Unscoped: every gap survives, including the orphan; the verdict is
	// still global.
	m, err := CoverageView(cov, facts, "full", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	rep := m.(*stipulatorv1.CoverageReport)
	if len(rep.GetGaps()) != 3 {
		t.Fatalf("unscoped view dropped a gap (orphan pruned?): %v", rep.GetGaps())
	}
	if len(rep.GetViolations()) != 2 || rep.GetGatePasses() {
		t.Fatalf("unscoped violations/verdict wrong: %v pass=%v", rep.GetViolations(), rep.GetGatePasses())
	}

	// An unknown view is refused, never rendered as an empty result
	// (REQ-mcp-views: a typo never reads as empty).
	if _, err := CoverageView(cov, facts, "nope", Scope{}); err == nil {
		t.Fatal("unknown coverage view accepted instead of refused")
	}
}

// TestCoverageSummaryPinsCountsAndGapState gives the summary counters
// teeth: every bucket count, the open-gap tally, and the prunable
// resolved-gap tally must move with the data, so a flipped counter or a
// resolved/open misclassification cannot pass silently. The prunable count
// is how the gate surfaces resolved gaps awaiting prune.
func TestCoverageSummaryPinsCountsAndGapState(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-views", "REQ-gap-resolved-pruned")
	cov := &coverage.Report{
		Requirements: []coverage.Requirement{
			{Id: "REQ-a", Bucket: coverage.Covered},
			{Id: "REQ-b", Bucket: coverage.Attested},
			{Id: "REQ-c", Bucket: coverage.Uncovered},
			{Id: "REQ-d", Bucket: coverage.Stale},
			{Id: "REQ-e", Bucket: coverage.Broken},
			{Id: "REQ-f", Bucket: coverage.Exempt},
		},
		// Asymmetric on purpose — two open, one resolved — so a
		// resolved/open misclassification changes at least one tally.
		Gaps: []coverage.Gap{
			{RequirementId: "REQ-c", State: coverage.Open},
			{RequirementId: "REQ-d", State: coverage.Open},
			{RequirementId: "REQ-a", State: coverage.Resolved},
		},
	}
	facts := Facts{Doc: map[string]string{}, Symbols: map[string][]string{}}
	m, err := CoverageView(cov, facts, "summary", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	sum := m.(*stipulatorv1.CoverageSummary)
	if sum.GetCovered() != 1 || sum.GetAttested() != 1 || sum.GetUncovered() != 1 ||
		sum.GetStale() != 1 || sum.GetBroken() != 1 || sum.GetExempt() != 1 {
		t.Fatalf("bucket counts each want 1: %+v", sum)
	}
	if sum.GetResolvedGapsPrunable() != 1 {
		t.Fatalf("resolved_gaps_prunable = %d, want 1 (only REQ-a's gap is resolved)", sum.GetResolvedGapsPrunable())
	}
	if sum.GetGapsOpen() != 2 {
		t.Fatalf("gaps_open = %d, want 2 (the resolved gap is excluded)", sum.GetGapsOpen())
	}
}

// TestScopeValidate pins the typo rule: unknown vocabulary refuses
// before filtering, so a misspelling never reads as an empty result.
func TestScopeValidate(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-views")
	if err := (Scope{Bucket: "redish"}).Validate(); err == nil || !strings.Contains(err.Error(), `unknown bucket "redish"`) {
		t.Fatalf("bucket typo accepted: %v", err)
	}
	if err := (Scope{Filter: "[bad"}).Validate(); err == nil {
		t.Fatal("bad glob accepted")
	}
	if err := (Scope{Bucket: "Uncovered", Filter: "REQ-*"}).Validate(); err != nil {
		t.Fatalf("valid scope refused: %v", err)
	}
	// Summary counts respect scope while the verdict stays global.
	cov := &coverage.Report{
		Requirements: []coverage.Requirement{
			{Id: "REQ-s-a", Bucket: coverage.Covered},
			{Id: "REQ-s-b", Bucket: coverage.Uncovered},
		},
		Violations:      []string{"REQ-s-b"},
		Gaps:            []coverage.Gap{{RequirementId: "REQ-s-b", State: coverage.Open}},
		PolicyOverrides: []string{"(behavior, MUST) -> EXEMPT"},
	}
	facts := Facts{Doc: map[string]string{"REQ-s-a": "specs/a.md", "REQ-s-b": "specs/b.md"}, Symbols: map[string][]string{}}
	m, err := CoverageView(cov, facts, "summary", Scope{Path: "specs/a.md"})
	if err != nil {
		t.Fatal(err)
	}
	sum := m.(*stipulatorv1.CoverageSummary)
	if sum.GetCovered() != 1 || sum.GetUncovered() != 0 || len(sum.GetViolations()) != 0 {
		t.Fatalf("scoped summary: %v", sum)
	}
	if sum.GetGatePasses() {
		t.Fatal("scoped slice hid the global verdict")
	}
	// Active policy overrides ride every view — the summary least of all
	// may apply one silently. Gaps count within the scope.
	if len(sum.GetPolicyOverrides()) != 1 {
		t.Fatalf("override hidden from the summary: %v", sum)
	}
	if sum.GetGapsOpen() != 0 { // REQ-s-b's gap is outside the scope
		t.Fatalf("gaps_open not scoped: %v", sum)
	}
	unscoped, err := CoverageView(cov, facts, "summary", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if unscoped.(*stipulatorv1.CoverageSummary).GetGapsOpen() != 1 {
		t.Fatalf("unscoped gaps_open wrong: %v", unscoped)
	}
}
