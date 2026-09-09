package views

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/stipulate"
)

// Both summaries count the dangling pointers, and an id scope narrows
// the count to the scoped requirements on both — the coverage summary
// through the in-memory tally, the check summary through the wire rows
// the scope filter kept (REQ-change-enforcement-pointers, REQ-mcp-views).
//
//gofresh:pure
func TestSummariesCountDanglingPointersScoped(t *testing.T) {
	stipulate.Covers(t, "REQ-change-enforcement-pointers", "REQ-mcp-views")
	cov := &coverage.Report{
		Requirements:     []coverage.Requirement{{Id: "REQ-a", Bucket: coverage.Broken}, {Id: "REQ-b", Bucket: coverage.Broken}},
		DanglingPointers: []coverage.DanglingPointer{{Requirement: "REQ-a", Name: "TestA"}, {Requirement: "REQ-b", Name: "TestB"}, {Requirement: "REQ-b", Name: "TestC"}},
	}
	whole, err := CoverageView(cov, Facts{}, "summary", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if got := whole.(*stipulatorv1.CoverageSummary).GetPointersDangling(); got != 3 {
		t.Fatalf("whole coverage summary pointers = %d, want 3", got)
	}
	sliced := ScopeReport(cov, cov.Requirements[1:], map[string]bool{"REQ-b": true})
	if len(sliced.DanglingPointers) != 2 || sliced.DanglingPointers[0].Requirement != "REQ-b" {
		t.Fatalf("scoped report pointers = %v", sliced.DanglingPointers)
	}
	res := &stipulatorv1.CheckResult{}
	res.SetExecution(&stipulatorv1.ExecutionReport{})
	res.SetVerify(&stipulatorv1.VerifyReport{})
	res.SetCoverage(cov.Proto())
	m, err := CheckView(res, "summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.(*stipulatorv1.CheckSummary).GetPointersDangling(); got != 3 {
		t.Fatalf("whole check summary pointers = %d, want 3", got)
	}
	m, err = CheckView(res, "summary", []string{"REQ-b"})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.(*stipulatorv1.CheckSummary).GetPointersDangling(); got != 2 {
		t.Fatalf("scoped check summary pointers = %d, want 2", got)
	}
}
