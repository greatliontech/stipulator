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
