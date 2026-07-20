package views

import (
	"fmt"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

func redRow(id string) *stipulatorv1.RequirementCoverage {
	r := &stipulatorv1.RequirementCoverage{}
	r.SetId(id)
	r.SetBucket(stipulatorv1.Bucket_BUCKET_UNCOVERED)
	r.SetReasons([]string{"needs a witness"})
	return r
}

// The summary aggregates per-test reason maps to distinct-reason
// histograms, caps red rows with a stated remainder, and reduces
// diagnostics to headings — bounded by construction while the full view
// carries everything (REQ-mcp-response-contract, REQ-mcp-views).
//
//gofresh:pure
func TestCheckViewSummaryBounds(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-response-contract", "REQ-mcp-views")
	res := &stipulatorv1.CheckResult{}
	res.SetPassed(false)
	reasons := map[string]string{}
	for i := 0; i < 500; i++ {
		reasons[fmt.Sprintf("pkg.Test%d", i)] = "GOCACHE drift"
	}
	reasons["pkg.TestOdd"] = "ephemeral /tmp input"
	res.SetUncacheableReasons(reasons)
	cov := &stipulatorv1.CoverageReport{}
	var rows []*stipulatorv1.RequirementCoverage
	for i := 0; i < redRowCap+7; i++ {
		rows = append(rows, redRow(fmt.Sprintf("REQ-x-%03d", i)))
	}
	cov.SetRequirements(rows)
	var violationIDs []string
	for _, r := range rows {
		violationIDs = append(violationIDs, r.GetId())
	}
	cov.SetViolations(violationIDs)
	res.SetCoverage(cov)
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetPackage("example.com/m/red")
	d.SetTest("TestRed")
	d.SetOutput("massive retained output\n")
	res.SetWitnessDiagnostics([]*stipulatorv1.FailureDiagnostic{d})

	m, err := CheckView(res, "summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := m.(*stipulatorv1.CheckSummary)
	if got := sum.GetUncacheableReasonCounts(); got["GOCACHE drift"] != 500 || got["ephemeral /tmp input"] != 1 {
		t.Fatalf("histogram = %v", got)
	}
	if len(sum.GetReds()) != redRowCap || sum.GetRedsOmitted() != 7 {
		t.Fatalf("reds = %d, omitted = %d", len(sum.GetReds()), sum.GetRedsOmitted())
	}
	if len(sum.GetViolations()) != redRowCap || sum.GetViolationsOmitted() != 7 {
		t.Fatalf("violations = %d, omitted = %d (the red cap must not be undermined one field away)",
			len(sum.GetViolations()), sum.GetViolationsOmitted())
	}
	if got := sum.GetWitnessFailureHeadings(); len(got) != 1 || got[0] != "failed: example.com/m/red.TestRed" {
		t.Fatalf("headings = %v (bodies must not ride the summary)", got)
	}

	if _, err := CheckView(res, "reds", nil); err == nil {
		t.Fatal("unknown view accepted")
	}
	if full, err := CheckView(res, "full", nil); err != nil || full != res {
		t.Fatalf("full view = %v, %v", full, err)
	}
}

// The summary's derived judgments match their canonical sources: an
// empty invocation list reads unhealthy exactly as the verdict judges
// it, and the binding counts are verification's own axes.
//
//gofresh:pure
func TestCheckViewSummaryMatchesCanonicalJudgments(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-views", "REQ-mcp-response-contract")
	res := &stipulatorv1.CheckResult{}
	res.SetPassed(false)
	res.SetSuiteHealthJudged(true)
	res.SetExecution(&stipulatorv1.ExecutionReport{})
	unresolvedUnpinned := &stipulatorv1.BindingResult{}
	unresolvedUnpinned.SetRequirementId("REQ-a")
	unresolvedUnpinned.SetResolution(stipulatorv1.Resolution_RESOLUTION_NOT_FOUND)
	mismatch := &stipulatorv1.BindingResult{}
	mismatch.SetRequirementId("REQ-b")
	mismatch.SetContentPinned(true)
	mismatch.SetResolution(stipulatorv1.Resolution_RESOLUTION_RESOLVED)
	mismatch.SetShape(stipulatorv1.ShapeState_SHAPE_STATE_MISMATCH)
	vr := &stipulatorv1.VerifyReport{}
	vr.SetResults([]*stipulatorv1.BindingResult{unresolvedUnpinned, mismatch})
	res.SetVerify(vr)

	m, err := CheckView(res, "summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := m.(*stipulatorv1.CheckSummary)
	if sum.GetSuiteHealthy() {
		t.Fatal("empty invocation list read healthy — the summary cannot explain its own failed verdict")
	}
	// Verification's axes: the unresolved row is broken AND stale (pin
	// unset), the mismatched row its own axis — never folded.
	if sum.GetBindingsBroken() != 1 || sum.GetBindingsStale() != 1 || sum.GetBindingsShapeMismatch() != 1 {
		t.Fatalf("bindings broken=%d stale=%d mismatch=%d, want 1/1/1",
			sum.GetBindingsBroken(), sum.GetBindingsStale(), sum.GetBindingsShapeMismatch())
	}
}

// The histogram's key count is itself bounded: per-test-distinct
// reasons fold into a counted remainder instead of rebuilding the
// per-test map one key at a time.
//
//gofresh:pure
func TestCheckViewHistogramKeyCap(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-response-contract")
	res := &stipulatorv1.CheckResult{}
	reasons := map[string]string{}
	for i := 0; i < histogramKeyCap+20; i++ {
		reasons[fmt.Sprintf("pkg.Test%d", i)] = fmt.Sprintf("mid-run drift: mover-%d.txt", i)
	}
	res.SetUncacheableReasons(reasons)
	m, err := CheckView(res, "summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := m.(*stipulatorv1.CheckSummary).GetUncacheableReasonCounts()
	if len(got) != histogramKeyCap+1 {
		t.Fatalf("histogram keys = %d, want cap %d + remainder entry", len(got), histogramKeyCap+1)
	}
	var rest int32
	for k, n := range got {
		if strings.Contains(k, "more distinct reasons") {
			rest = n
		}
	}
	if rest != 20 {
		t.Fatalf("remainder entry counts %d, want 20", rest)
	}
}

// Scoping filters coverage rows, gaps, and violations together and
// never mutates the unscoped result; the verdict stays global.
//
//gofresh:pure
func TestCheckViewScopeFiltersWholeReport(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-views")
	res := &stipulatorv1.CheckResult{}
	res.SetPassed(false)
	cov := &stipulatorv1.CoverageReport{}
	cov.SetRequirements([]*stipulatorv1.RequirementCoverage{redRow("REQ-a-one"), redRow("REQ-b-two")})
	g := &stipulatorv1.GapReport{}
	g.SetRequirementId("REQ-b-two")
	cov.SetGaps([]*stipulatorv1.GapReport{g})
	cov.SetViolations([]string{"REQ-a-one", "REQ-b-two"})
	res.SetCoverage(cov)
	g.SetPath(".stipulator/gaps/b-two.textproto")
	res.SetPruneResidue([]string{".stipulator/gaps/b-two.textproto"})
	rowA := &stipulatorv1.BindingResult{}
	rowA.SetRequirementId("REQ-a-one")
	rowB := &stipulatorv1.BindingResult{}
	rowB.SetRequirementId("REQ-b-two")
	vr := &stipulatorv1.VerifyReport{}
	vr.SetResults([]*stipulatorv1.BindingResult{rowA, rowB})
	res.SetVerify(vr)

	m, err := CheckView(res, "full", []string{"REQ-a-one"})
	if err != nil {
		t.Fatal(err)
	}
	scoped := m.(*stipulatorv1.CheckResult)
	if rows := scoped.GetCoverage().GetRequirements(); len(rows) != 1 || rows[0].GetId() != "REQ-a-one" {
		t.Fatalf("rows = %v", rows)
	}
	if gaps := scoped.GetCoverage().GetGaps(); len(gaps) != 0 {
		t.Fatalf("out-of-scope gap kept: %v", gaps)
	}
	if v := scoped.GetCoverage().GetViolations(); len(v) != 1 || v[0] != "REQ-a-one" {
		t.Fatalf("violations = %v", v)
	}
	if scoped.GetPassed() {
		t.Fatal("scope flipped the global verdict")
	}
	if residue := scoped.GetPruneResidue(); len(residue) != 0 {
		t.Fatalf("out-of-scope requirement's record path kept: %v", residue)
	}
	if rows := scoped.GetVerify().GetResults(); len(rows) != 1 || rows[0].GetRequirementId() != "REQ-a-one" {
		t.Fatalf("out-of-scope binding rows kept: %v", rows)
	}
	// The unscoped result is untouched: scoping clones.
	if len(res.GetCoverage().GetRequirements()) != 2 || len(res.GetCoverage().GetGaps()) != 1 {
		t.Fatal("scoping mutated the input result")
	}
}

// The verify summary's package-failure values are retained runtime
// output: each entry is capped with the truncation stated
// (REQ-mcp-response-contract).
//
//gofresh:pure
func TestVerifySummaryCapsPackageFailureOutput(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-response-contract")
	vr := &verify.Report{PackageFailures: map[string]string{
		"example.com/m/broken": strings.Repeat("compiler noise\n", 1000),
		"example.com/m/short":  "one line\n",
	}}
	m, err := VerifyView(vr, Facts{}, "summary", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	got := m.(*stipulatorv1.VerifySummary).GetPackageFailures()
	long := got["example.com/m/broken"]
	if len(long) > packageFailureCap+100 || !strings.Contains(long, "truncated") {
		t.Fatalf("long failure not capped with a stated truncation (len=%d)", len(long))
	}
	if got["example.com/m/short"] != "one line\n" {
		t.Fatalf("short failure mangled: %q", got["example.com/m/short"])
	}
}
