package views

import (
	"fmt"
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/coverage"
	"google.golang.org/protobuf/proto"
)

// redRowCap bounds the summary's red-row list; the remainder rides
// reds_omitted so the truncation is never silent
// (REQ-mcp-response-contract).
const redRowCap = 25

// CheckView projects one check result into the requested view: the
// summary (default) or the full result message, either scoped to
// requirement identifiers. The view itself never alters the verdict it
// projects — a scoped check RUN carries its own flagged-partial verdict
// (REQ-check-verdict), the gate's stays global under any view scope —
// a scoped slice with no in-scope violation says nothing about whether
// the tree passes (REQ-mcp-views). An unknown view word is refused, so
// a typo never reads as an empty result.
func CheckView(res *stipulatorv1.CheckResult, view string, ids []string) (proto.Message, error) {
	scoped := res
	if len(ids) > 0 {
		scoped = scopeCheck(res, ids)
	}
	switch view {
	case "", "summary":
		return checkSummary(scoped), nil
	case "full":
		return scoped, nil
	default:
		return nil, fmt.Errorf("unknown view %q (summary, full)", view)
	}
}

// scopeCheck narrows the whole result to the given requirements:
// coverage rows, gap rows, and violations filter together, so scoped
// triage is never polluted by out-of-scope entries. Test-keyed surfaces
// (reason maps, diagnostics) stay global — tests are not requirements,
// and dropping them on an id scope would silently hide evidence.
func scopeCheck(res *stipulatorv1.CheckResult, ids []string) *stipulatorv1.CheckResult {
	keep := make(map[string]bool, len(ids))
	for _, id := range ids {
		keep[id] = true
	}
	out := proto.CloneOf(res)
	cov := out.GetCoverage()
	if cov == nil {
		return out
	}
	var rows []*stipulatorv1.RequirementCoverage
	for _, r := range cov.GetRequirements() {
		if keep[r.GetId()] {
			rows = append(rows, r)
		}
	}
	cov.SetRequirements(rows)
	var gaps []*stipulatorv1.GapReport
	for _, g := range cov.GetGaps() {
		if keep[g.GetRequirementId()] {
			gaps = append(gaps, g)
		}
	}
	cov.SetGaps(gaps)
	var violations []string
	for _, v := range cov.GetViolations() {
		if keep[v] {
			violations = append(violations, v)
		}
	}
	cov.SetViolations(violations)
	var dangling []*stipulatorv1.DanglingPointer
	for _, p := range cov.GetDanglingPointers() {
		if keep[p.GetRequirementId()] {
			dangling = append(dangling, p)
		}
	}
	cov.SetDanglingPointers(dangling)
	// Residue paths join to requirements through the unfiltered gap rows
	// — the scope narrows the WHOLE report (REQ-mcp-views), so an
	// out-of-scope requirement's record path must not pollute scoped
	// triage.
	pathKeep := map[string]bool{}
	for _, g := range res.GetCoverage().GetGaps() {
		if keep[g.GetRequirementId()] {
			pathKeep[g.GetPath()] = true
		}
	}
	var residue []string
	for _, p := range out.GetPruneResidue() {
		if pathKeep[p] {
			residue = append(residue, p)
		}
	}
	out.SetPruneResidue(residue)
	if v := out.GetVerify(); v != nil {
		var rows []*stipulatorv1.BindingResult
		for _, r := range v.GetResults() {
			if keep[r.GetRequirementId()] {
				rows = append(rows, r)
			}
		}
		v.SetResults(rows)
	}
	return out
}

// checkSummary is the bounded projection: counts, blocker rows, capped
// red rows, and heading-only diagnostics.
func checkSummary(res *stipulatorv1.CheckResult) *stipulatorv1.CheckSummary {
	out := &stipulatorv1.CheckSummary{}
	out.SetPassed(res.GetPassed())
	out.SetSuiteHealthJudged(res.GetSuiteHealthJudged())
	if ex := res.GetExecution(); ex != nil {
		// The verdict's own judgment, so the summary can always explain
		// its failed verdict — never a second reading of the dispositions.
		out.SetSuiteHealthy(golang.SuiteHealthy(ex))
	}
	out.SetPolicyNotices(res.GetPolicyNotices())
	out.SetTestsServed(res.GetTestsServed())
	out.SetTestsExecuted(res.GetTestsExecuted())
	out.SetTestsUncacheable(res.GetTestsUncacheable())
	out.SetTestsOutsidePolicy(res.GetTestsOutsidePolicy())
	out.SetWitnessSelectionProblem(res.GetWitnessSelectionProblem())
	out.SetScopePartial(res.GetScopePartial())
	out.SetScopeIds(res.GetScopeIds())
	uncacheable, uncacheableOmitted := blockerRows(res.GetUncacheableReasons())
	out.SetUncacheableBlockers(uncacheable)
	out.SetUncacheableReasonsOmitted(uncacheableOmitted)
	executed, executedOmitted := blockerRows(res.GetExecutedReasons())
	out.SetExecutedBlockers(executed)
	out.SetExecutedReasonsOmitted(executedOmitted)
	out.SetCompileProblems(res.GetCompileProblems())
	if p := res.GetPolicyProblem(); p != nil {
		out.SetPolicyProblem(p)
	}
	if v := res.GetVerify(); v != nil {
		out.SetVerifyProblems(int32(len(v.GetProblems())))
		// Verification's own tally, projected onto the wire report once
		// (verify.Report.Tally): read, never recounted from the rows.
		out.SetBindingsStale(v.GetStale())
		out.SetBindingsBroken(v.GetBroken())
		out.SetBindingsShapeMismatch(v.GetShapeMismatch())
	}
	if cov := res.GetCoverage(); cov != nil {
		out.SetGatePasses(cov.GetGatePasses())
		var reds []*stipulatorv1.CheckRedRow
		omitted := int32(0)
		blocked := int32(0)
		scopeBlocked := int32(0)
		// The one ladder classifies every red row; the bounded summary
		// folds the rows restating a result-level cause into counts —
		// the cause stated once, the real reds visible, the folded rows
		// on the full view (REQ-check-witness-selection) — and caps the
		// rest.
		for _, r := range coverage.RedRows(res) {
			switch r.Fold {
			case coverage.RedPolicyBlocked:
				blocked++
				continue
			case coverage.RedScopeBlocked:
				scopeBlocked++
				continue
			}
			if len(reds) == redRowCap {
				omitted++
				continue
			}
			row := &stipulatorv1.CheckRedRow{}
			row.SetId(r.Id)
			row.SetBucket(r.Bucket)
			if len(r.Reasons) > 0 {
				row.SetReason(r.Reasons[0])
			}
			reds = append(reds, row)
		}
		out.SetReds(reds)
		out.SetRedsOmitted(omitted)
		out.SetRedsPolicyBlocked(blocked)
		out.SetRedsScopeBlocked(scopeBlocked)
		tally := coverage.GapCountsWire(cov.GetGaps())
		out.SetGapsOpen(int32(tally.Open))
		out.SetGapsDue(int32(tally.Due))
		out.SetGapsResolved(int32(tally.Resolved))
		out.SetGapsContradicted(int32(tally.Contradicted))
		out.SetPointersDangling(int32(len(cov.GetDanglingPointers())))
		violations := cov.GetViolations()
		if len(violations) > redRowCap {
			out.SetViolationsOmitted(int32(len(violations) - redRowCap))
			violations = violations[:redRowCap]
		}
		out.SetViolations(violations)
	}
	out.SetPruneResidue(res.GetPruneResidue())
	var headings []string
	for _, d := range res.GetWitnessDiagnostics() {
		headings = append(headings, DiagnosticHeading(d))
	}
	if ex := res.GetExecution(); ex != nil {
		for _, d := range ex.GetDiagnostics() {
			headings = append(headings, DiagnosticHeading(d))
		}
	}
	if len(headings) > HeadingCap {
		out.SetWitnessFailureHeadingsOmitted(int32(len(headings) - HeadingCap))
		headings = headings[:HeadingCap]
	}
	out.SetWitnessFailureHeadings(headings)
	out.SetWitnessPublicationDegraded(res.GetWitnessPublicationDegraded())
	return out
}

// HeadingCap bounds the summary's diagnostic-heading list; the
// remainder rides witness_failure_headings_omitted, and the text
// digest counts against the same bound.
const HeadingCap = 50

// blockerRowCap bounds the summary's blocker rows: the top reasons are
// the actionable ones, the remainder is a count - the raw per-test
// maps ride only the full view.
const blockerRowCap = 5

// blockerRows reduces a per-test reason map to the actionable form:
// the top reasons by witness count, one exemplar test each, ordered by
// count descending then reason ascending; the exemplar is the
// lexicographically-smallest affected test so identical runs render
// identically. The second return counts the distinct reasons the cap
// dropped.
func blockerRows(reasons map[string]string) ([]*stipulatorv1.CheckBlockerRow, int32) {
	if len(reasons) == 0 {
		return nil, 0
	}
	counts := map[string]int32{}
	exemplar := map[string]string{}
	for test, why := range reasons {
		counts[why]++
		if e, ok := exemplar[why]; !ok || test < e {
			exemplar[why] = test
		}
	}
	type entry struct {
		why string
		n   int32
	}
	entries := make([]entry, 0, len(counts))
	for why, n := range counts {
		entries = append(entries, entry{why, n})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].n != entries[j].n {
			return entries[i].n > entries[j].n
		}
		return entries[i].why < entries[j].why
	})
	omitted := int32(0)
	if len(entries) > blockerRowCap {
		omitted = int32(len(entries) - blockerRowCap)
		entries = entries[:blockerRowCap]
	}
	rows := make([]*stipulatorv1.CheckBlockerRow, 0, len(entries))
	for _, e := range entries {
		row := &stipulatorv1.CheckBlockerRow{}
		row.SetReason(e.why)
		row.SetWitnesses(e.n)
		row.SetExemplar(exemplar[e.why])
		rows = append(rows, row)
	}
	return rows, omitted
}

// DiagnosticHeading names one failure diagnostic's unit and disposition
// without its retained output — the one heading every face renders: the
// summaries carry it alone, the human rendering puts the retained output
// under it. A degraded execution is named distinctly from an assertion
// failure: conflating them would leave an environment-induced failure
// and a real regression indistinguishable (REQ-check-diagnostics).
func DiagnosticHeading(d *stipulatorv1.FailureDiagnostic) string {
	subject := d.GetInvocation()
	if p := d.GetPackage(); p != "" {
		subject = p
	}
	if t := d.GetTest(); t != "" {
		subject = d.GetPackage() + "." + t
	}
	word := "failed"
	switch d.GetDisposition() {
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED:
		word = "degraded"
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED:
		word = "build failed"
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT:
		word = "timeout"
	}
	return word + ": " + subject
}
