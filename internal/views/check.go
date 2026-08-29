package views

import (
	"fmt"
	"sort"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
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
		// Mirrors golang.SuiteHealthy's arms exactly — including
		// unhealthy on an empty invocation list, which is how the verdict
		// itself judges it; a diverging healthy=true here would leave the
		// summary unable to explain its own failed verdict.
		healthy := len(ex.GetInvocations()) > 0
		for _, inv := range ex.GetInvocations() {
			if inv.GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
				healthy = false
			}
		}
		out.SetSuiteHealthy(healthy)
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
		// Verification's own axes, never a third classification: stale
		// counts every unpinned row, broken counts unresolved symbols,
		// shape mismatch its own axis — matching VerifySummary.
		var stale, broken, mismatch int32
		for _, r := range v.GetResults() {
			if !r.GetContentPinned() {
				stale++
			}
			if r.GetResolution() == stipulatorv1.Resolution_RESOLUTION_NOT_FOUND {
				broken++
			}
			if r.GetShape() == stipulatorv1.ShapeState_SHAPE_STATE_MISMATCH {
				mismatch++
			}
		}
		out.SetBindingsStale(stale)
		out.SetBindingsBroken(broken)
		out.SetBindingsShapeMismatch(mismatch)
	}
	if cov := res.GetCoverage(); cov != nil {
		out.SetGatePasses(cov.GetGatePasses())
		var reds []*stipulatorv1.CheckRedRow
		omitted := int32(0)
		blocked := int32(0)
		scopeBlocked := int32(0)
		for _, r := range cov.GetRequirements() {
			switch r.GetBucket() {
			case stipulatorv1.Bucket_BUCKET_UNCOVERED, stipulatorv1.Bucket_BUCKET_STALE, stipulatorv1.Bucket_BUCKET_BROKEN:
				// Rows red solely because of the witness-selection
				// boundary restate the one result-level diagnostic; when
				// that diagnostic fired, they fold into a count so the
				// summary carries the cause once and the real reds stay
				// visible (REQ-check-witness-selection). The rows ride
				// the full view.
				if res.GetWitnessSelectionProblem() != "" && r.GetWitnessSelectionBlocked() {
					blocked++
					continue
				}
				// Scope-boundary rows restate the result's partial flag
				// the same way: folded to a count on scoped passes, the
				// cause stated once by scope_partial, the rows on the
				// full view.
				if res.GetScopePartial() && r.GetScopeBlocked() {
					scopeBlocked++
					continue
				}
				if len(reds) == redRowCap {
					omitted++
					continue
				}
				row := &stipulatorv1.CheckRedRow{}
				row.SetId(r.GetId())
				row.SetBucket(strings.ToLower(strings.TrimPrefix(r.GetBucket().String(), "BUCKET_")))
				if rs := r.GetReasons(); len(rs) > 0 {
					row.SetReason(rs[0])
				}
				reds = append(reds, row)
			}
		}
		out.SetReds(reds)
		out.SetRedsOmitted(omitted)
		out.SetRedsPolicyBlocked(blocked)
		out.SetRedsScopeBlocked(scopeBlocked)
		var open, due, resolved int32
		for _, g := range cov.GetGaps() {
			switch g.GetState() {
			case stipulatorv1.GapState_GAP_STATE_DUE:
				due++
			case stipulatorv1.GapState_GAP_STATE_RESOLVED:
				resolved++
			default:
				open++
			}
		}
		out.SetGapsOpen(open)
		out.SetGapsDue(due)
		out.SetGapsResolved(resolved)
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
		headings = append(headings, diagnosticHeadingWord(d))
	}
	if ex := res.GetExecution(); ex != nil {
		for _, d := range ex.GetDiagnostics() {
			headings = append(headings, diagnosticHeadingWord(d))
		}
	}
	if len(headings) > headingCap {
		out.SetWitnessFailureHeadingsOmitted(int32(len(headings) - headingCap))
		headings = headings[:headingCap]
	}
	out.SetWitnessFailureHeadings(headings)
	out.SetWitnessPublicationDegraded(res.GetWitnessPublicationDegraded())
	return out
}

// headingCap bounds the summary's diagnostic-heading list.
const headingCap = 50

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

// diagnosticHeadingWord names one diagnostic's unit and disposition
// without its retained output — the summary's heading form; the bodies
// ride only the full view.
func diagnosticHeadingWord(d *stipulatorv1.FailureDiagnostic) string {
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
