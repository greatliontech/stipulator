package views

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

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

// The summary reduces per-test reason maps to top-blocker rows on both
// axes (uncacheable and re-executed) with count ties broken by reason,
// caps red rows with a stated remainder, and reduces diagnostics to
// headings — bounded by construction while the full view carries
// everything (REQ-mcp-response-contract, REQ-mcp-views).
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
	res.SetExecutedReasons(map[string]string{
		"pkg.TestR1": "content changed",
		"pkg.TestR2": "content changed",
		"pkg.TestR3": "binding moved",
		"pkg.TestR4": "binding moved",
	})
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
	blockers := sum.GetUncacheableBlockers()
	if len(blockers) != 2 || blockers[0].GetReason() != "GOCACHE drift" || blockers[0].GetWitnesses() != 500 ||
		blockers[1].GetReason() != "ephemeral /tmp input" || blockers[1].GetWitnesses() != 1 {
		t.Fatalf("blockers = %v", blockers)
	}
	if blockers[0].GetExemplar() == "" || blockers[1].GetExemplar() != "pkg.TestOdd" {
		t.Fatalf("blocker exemplars = %q, %q", blockers[0].GetExemplar(), blockers[1].GetExemplar())
	}
	if sum.GetUncacheableReasonsOmitted() != 0 {
		t.Fatalf("under-cap omitted = %d, want 0", sum.GetUncacheableReasonsOmitted())
	}
	// The re-executed axis reduces identically, with equal witness
	// counts tie-broken by reason ascending.
	executed := sum.GetExecutedBlockers()
	if len(executed) != 2 || executed[0].GetReason() != "binding moved" || executed[1].GetReason() != "content changed" ||
		executed[0].GetWitnesses() != 2 || executed[1].GetWitnesses() != 2 {
		t.Fatalf("executed blockers = %v, want count ties broken by reason", executed)
	}
	if executed[0].GetExemplar() != "pkg.TestR3" || executed[1].GetExemplar() != "pkg.TestR1" {
		t.Fatalf("executed exemplars = %q, %q", executed[0].GetExemplar(), executed[1].GetExemplar())
	}
	if sum.GetExecutedReasonsOmitted() != 0 {
		t.Fatalf("executed omitted = %d, want 0", sum.GetExecutedReasonsOmitted())
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

// The blocker rows are the actionable reduction: top reasons by
// witness count with one deterministic exemplar each, capped with a
// counted remainder — per-test-distinct reasons can never rebuild the
// per-test map one row at a time.
//
//gofresh:pure
func TestCheckViewBlockerRowsCapAndDeterminism(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-response-contract", "REQ-mcp-views")
	res := &stipulatorv1.CheckResult{}
	reasons := map[string]string{}
	// blockerRowCap+3 distinct reasons with descending weights, plus a
	// heavy shared reason whose exemplar must be the smallest test name.
	for i := 0; i < blockerRowCap+3; i++ {
		for j := 0; j <= i; j++ {
			reasons[fmt.Sprintf("pkg.Test%02d_%02d", i, j)] = fmt.Sprintf("mid-run drift: mover-%02d.txt", i)
		}
	}
	reasons["pkg.TestZz"] = "GOCACHE drift"
	reasons["pkg.TestAa"] = "GOCACHE drift"
	for i := 0; i < 40; i++ {
		reasons[fmt.Sprintf("pkg.TestG%02d", i)] = "GOCACHE drift"
	}
	res.SetUncacheableReasons(reasons)
	m, err := CheckView(res, "summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := m.(*stipulatorv1.CheckSummary)
	rows := sum.GetUncacheableBlockers()
	if len(rows) != blockerRowCap {
		t.Fatalf("blocker rows = %d, want the cap %d", len(rows), blockerRowCap)
	}
	if rows[0].GetReason() != "GOCACHE drift" || rows[0].GetWitnesses() != 42 {
		t.Fatalf("top blocker = %v, want the heaviest reason first", rows[0])
	}
	if rows[0].GetExemplar() != "pkg.TestAa" {
		t.Fatalf("exemplar = %q, want the lexicographically-smallest affected test", rows[0].GetExemplar())
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].GetWitnesses() > rows[i-1].GetWitnesses() {
			t.Fatalf("rows not ordered by witness count: %v", rows)
		}
	}
	if sum.GetUncacheableReasonsOmitted() != int32(blockerRowCap+3+1-blockerRowCap) {
		t.Fatalf("omitted = %d, want the dropped distinct-reason count", sum.GetUncacheableReasonsOmitted())
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

// Failure-diagnostic output is a retained runtime product: the verify
// summary omits it entirely and the full report carries the rows
// untruncated — bounded summary, lossless drill-down
// (REQ-mcp-response-contract).
//
//gofresh:pure
func TestVerifySummaryOmitsFailureDiagnosticOutput(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-response-contract")
	noise := strings.Repeat("compiler noise\n", 1000)
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetPackage("example.com/m/broken")
	d.SetOutput(noise)
	vr := &verify.Report{Diagnostics: []*stipulatorv1.FailureDiagnostic{d}}
	m, err := VerifyView(vr, Facts{}, "summary", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	sum := m.(*stipulatorv1.VerifySummary)
	raw, err := proto.Marshal(sum)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "compiler noise") {
		t.Fatalf("summary payload carries retained diagnostic output (%d bytes)", len(raw))
	}
	if h := sum.GetWitnessFailureHeadings(); len(h) != 1 || h[0] != "failed: example.com/m/broken" {
		t.Fatalf("summary headings = %v", h)
	}
	m, err = VerifyView(vr, Facts{}, "bindings", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	rows := m.(*stipulatorv1.VerifyReport).GetWitnessDiagnostics()
	if len(rows) != 1 || rows[0].GetOutput() != noise {
		t.Fatalf("full report lost or truncated the diagnostic row: %v", rows)
	}

	// Past the cap, the omitted remainder is counted — a truncation is
	// never silent.
	var many []*stipulatorv1.FailureDiagnostic
	for i := 0; i < headingCap+3; i++ {
		many = append(many, d)
	}
	m, err = VerifyView(&verify.Report{Diagnostics: many}, Facts{}, "summary", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	sum = m.(*stipulatorv1.VerifySummary)
	if len(sum.GetWitnessFailureHeadings()) != headingCap || sum.GetWitnessFailureHeadingsOmitted() != 3 {
		t.Fatalf("capped headings = %d, omitted = %d", len(sum.GetWitnessFailureHeadings()), sum.GetWitnessFailureHeadingsOmitted())
	}
}

// When the result-level witness-selection diagnostic fires, rows red
// solely on that boundary fold into reds_policy_blocked behind it; a red
// for any other reason stays a visible row, and without the diagnostic
// the marker alone folds nothing (REQ-check-witness-selection).
//
// The summary mirrors the scoped-class flag and scope ids, and folds
// requirements red solely on the scope boundary to a count — the cause
// stated once by scope_partial, the rows riding the full view. Without
// the partial flag the row marker folds nothing.
//
//gofresh:pure
func TestCheckSummaryMirrorsScopeAndFoldsScopeBlockedRows(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict", "REQ-report-check-result")
	blocked := &stipulatorv1.RequirementCoverage{}
	blocked.SetId("REQ-blocked")
	blocked.SetBucket(stipulatorv1.Bucket_BUCKET_BROKEN)
	blocked.SetReasons([]string{"bound test example.com/m.TestB not executed - outside the check's id scope"})
	blocked.SetScopeBlocked(true)
	failing := &stipulatorv1.RequirementCoverage{}
	failing.SetId("REQ-failing")
	failing.SetBucket(stipulatorv1.Bucket_BUCKET_BROKEN)
	failing.SetReasons([]string{"bound test failed"})
	cov := &stipulatorv1.CoverageReport{}
	cov.SetRequirements([]*stipulatorv1.RequirementCoverage{blocked, failing})
	res := &stipulatorv1.CheckResult{}
	res.SetCoverage(cov)
	res.SetScopePartial(true)
	res.SetScopeIds([]string{"REQ-failing"})

	view, err := CheckView(res, "summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := view.(*stipulatorv1.CheckSummary)
	if !sum.GetScopePartial() || len(sum.GetScopeIds()) != 1 || sum.GetScopeIds()[0] != "REQ-failing" {
		t.Fatalf("scope mirror lost: partial=%v ids=%v", sum.GetScopePartial(), sum.GetScopeIds())
	}
	if sum.GetRedsScopeBlocked() != 1 {
		t.Fatalf("reds_scope_blocked = %d, want 1", sum.GetRedsScopeBlocked())
	}
	ids := map[string]bool{}
	for _, r := range sum.GetReds() {
		ids[r.GetId()] = true
	}
	if ids["REQ-blocked"] || !ids["REQ-failing"] {
		t.Fatalf("summary reds = %v, want the scope-blocked row folded and the real red visible", ids)
	}

	// The full view keeps every row — the fold is a projection.
	full, err := CheckView(res, "full", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(full.(*stipulatorv1.CheckResult).GetCoverage().GetRequirements()); got != 2 {
		t.Fatalf("full view rows = %d, want 2", got)
	}

	// Row marker without the result-level partial flag folds nothing —
	// a global pass never hides a red behind a stale marker.
	res.SetScopePartial(false)
	view, err = CheckView(res, "summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	sum = view.(*stipulatorv1.CheckSummary)
	if sum.GetRedsScopeBlocked() != 0 || len(sum.GetReds()) != 2 {
		t.Fatalf("no-flag fold: blocked=%d reds=%d, want 0 and 2", sum.GetRedsScopeBlocked(), len(sum.GetReds()))
	}
}

func TestCheckSummaryFoldsPolicyBlockedRows(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	blocked := &stipulatorv1.RequirementCoverage{}
	blocked.SetId("REQ-blocked")
	blocked.SetBucket(stipulatorv1.Bucket_BUCKET_BROKEN)
	blocked.SetReasons([]string{"bound test x is outside the policy's witness-eligible selection - ..."})
	blocked.SetWitnessSelectionBlocked(true)
	failing := &stipulatorv1.RequirementCoverage{}
	failing.SetId("REQ-failing")
	failing.SetBucket(stipulatorv1.Bucket_BUCKET_BROKEN)
	failing.SetReasons([]string{"bound test y failed"})
	cov := &stipulatorv1.CoverageReport{}
	cov.SetRequirements([]*stipulatorv1.RequirementCoverage{blocked, failing, redRow("REQ-unbound")})
	res := &stipulatorv1.CheckResult{}
	res.SetCoverage(cov)
	res.SetWitnessSelectionProblem("the witness-eligible selection covered no expected witness: ...")

	view, err := CheckView(res, "summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := view.(*stipulatorv1.CheckSummary)
	if sum.GetRedsPolicyBlocked() != 1 {
		t.Fatalf("reds_policy_blocked = %d, want 1", sum.GetRedsPolicyBlocked())
	}
	ids := map[string]bool{}
	for _, r := range sum.GetReds() {
		ids[r.GetId()] = true
	}
	if ids["REQ-blocked"] || !ids["REQ-failing"] || !ids["REQ-unbound"] {
		t.Fatalf("summary reds = %v, want the blocked row folded and the real reds visible", ids)
	}

	// The full view keeps every row — the fold is a projection.
	full, err := CheckView(res, "full", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(full.(*stipulatorv1.CheckResult).GetCoverage().GetRequirements()); got != 3 {
		t.Fatalf("full view rows = %d, want 3", got)
	}

	// Marker without the result-level diagnostic folds nothing.
	res.SetWitnessSelectionProblem("")
	view, err = CheckView(res, "summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	sum = view.(*stipulatorv1.CheckSummary)
	if sum.GetRedsPolicyBlocked() != 0 || len(sum.GetReds()) != 3 {
		t.Fatalf("no-diagnostic fold: blocked=%d reds=%d, want 0 and 3", sum.GetRedsPolicyBlocked(), len(sum.GetReds()))
	}
}

// Policy-tier notices survive the summary projection whole - they are
// few and load-bearing, and dropping them at the default view would
// hide the degradation exactly where operators read
// (REQ-check-policy-notices).
//
//gofresh:pure
func TestCheckSummaryCarriesPolicyNotices(t *testing.T) {
	stipulate.Covers(t, "REQ-check-policy-notices")
	res := &stipulatorv1.CheckResult{}
	res.SetPolicyNotices([]string{`invocation "tagged": toolchain-selection audit: selection "dup" is unwalked`})
	out := checkSummary(res)
	if got := out.GetPolicyNotices(); len(got) != 1 || got[0] != res.GetPolicyNotices()[0] {
		t.Fatalf("summary notices = %v", got)
	}
}
