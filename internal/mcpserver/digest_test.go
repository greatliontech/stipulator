package mcpserver

import (
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// A view's text content is a bounded actionable digest: the verdict line
// plus capped rows naming what to repair, with counted omissions - so a
// client exposing text content only never needs a CLI fallback
// (REQ-mcp-response-contract).
func TestViewDigestsCarryActionRowsBounded(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-response-contract")
	report := &stipulatorv1.CoverageReport{}
	var rows []*stipulatorv1.RequirementCoverage
	for i := 0; i < digestRowCap+3; i++ {
		r := &stipulatorv1.RequirementCoverage{}
		r.SetId("REQ-digest-" + strings.Repeat("x", i+1))
		r.SetBucket(stipulatorv1.Bucket_BUCKET_STALE)
		r.SetReasons([]string{"binding has a stale content pin"})
		rows = append(rows, r)
	}
	report.SetRequirements(rows)
	text := viewLine("gate", report)
	if !strings.Contains(text, "REQ-digest-x [stale]: binding has a stale content pin") {
		t.Fatalf("digest lost the action row:\n%s", text)
	}
	if !strings.Contains(text, "and 3 more") {
		t.Fatalf("digest truncation not counted:\n%s", text)
	}
	if got := strings.Count(text, "\n"); got != digestRowCap+1 {
		t.Fatalf("digest carries %d rows past the cap:\n%s", got, text)
	}

	verifyReport := &stipulatorv1.VerifyReport{}
	binding := &stipulatorv1.BindingResult{}
	binding.SetRequirementId("REQ-digest-bind")
	binding.SetSymbol("example.com/pkg.TestThing")
	binding.SetResolution(stipulatorv1.Resolution_RESOLUTION_NOT_FOUND)
	binding.SetTestOutcome(stipulatorv1.TestOutcome_TEST_OUTCOME_NOT_RUN)
	verifyReport.SetResults([]*stipulatorv1.BindingResult{binding})
	text = viewLine("verify", verifyReport)
	if !strings.Contains(text, "REQ-digest-bind") || !strings.Contains(text, "example.com/pkg.TestThing") {
		t.Fatalf("verify bindings digest is not actionable:\n%s", text)
	}
	// Multi-word enum values keep their qualifiers: a broken row must
	// never read healthy ("not found" inverted to "found").
	if !strings.Contains(text, "[not found, not run]") {
		t.Fatalf("enum words inverted in the digest row:\n%s", text)
	}
	if strings.Contains(text, "structured content carries the payload") {
		t.Fatalf("placeholder text survived:\n%s", text)
	}
}
