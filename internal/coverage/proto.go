package coverage

import (
	"fmt"
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// Proto renders the report as its wire message.
func (r *Report) Proto() *stipulatorv1.CoverageReport {
	out := &stipulatorv1.CoverageReport{}
	var reqs []*stipulatorv1.RequirementCoverage
	for _, rc := range r.Requirements {
		m := &stipulatorv1.RequirementCoverage{}
		m.SetId(rc.Id)
		m.SetKind(rc.Kind)
		m.SetKeyword(rc.Keyword)
		m.SetBucket(bucketProto[rc.Bucket])
		m.SetReasons(rc.Reasons)
		m.SetWitnessSelectionBlocked(rc.WitnessSelectionBlocked)
		m.SetScopeBlocked(rc.ScopeBlocked)
		reqs = append(reqs, m)
	}
	out.SetRequirements(reqs)
	out.SetPolicyOverrides(r.PolicyOverrides)
	var dangling []*stipulatorv1.DanglingPointer
	for _, p := range r.DanglingPointers {
		d := &stipulatorv1.DanglingPointer{}
		d.SetRequirementId(p.Requirement)
		d.SetName(p.Name)
		dangling = append(dangling, d)
	}
	out.SetDanglingPointers(dangling)

	out.SetGaps(r.GapsProto())
	out.SetViolations(r.Violations)
	out.SetGatePasses(r.GatePasses())
	return out
}

// GapsProto renders the report's evaluated gap rows as their wire
// messages — the rows a listing reads without materializing the whole
// report.
func (r *Report) GapsProto() []*stipulatorv1.GapReport {
	var gaps []*stipulatorv1.GapReport
	for _, g := range r.Gaps {
		m := &stipulatorv1.GapReport{}
		m.SetPath(g.Path)
		m.SetRequirementId(g.RequirementId)
		m.SetState(gapProto[g.State])
		m.SetReason(g.Reason)
		m.SetCondition(g.Condition)
		m.SetFired(g.Fired)
		m.SetContradicted(g.Contradicted)
		m.SetStaleConsent(g.StaleConsent)
		gaps = append(gaps, m)
	}
	return gaps
}

var bucketProto = map[Bucket]stipulatorv1.Bucket{
	Attested:  stipulatorv1.Bucket_BUCKET_ATTESTED,
	Uncovered: stipulatorv1.Bucket_BUCKET_UNCOVERED,
	Stale:     stipulatorv1.Bucket_BUCKET_STALE,
	Broken:    stipulatorv1.Bucket_BUCKET_BROKEN,
	Covered:   stipulatorv1.Bucket_BUCKET_COVERED,
	Exempt:    stipulatorv1.Bucket_BUCKET_EXEMPT,
	Partial:   stipulatorv1.Bucket_BUCKET_PARTIAL,
}

var gapProto = map[GapState]stipulatorv1.GapState{
	Open:     stipulatorv1.GapState_GAP_STATE_OPEN,
	Due:      stipulatorv1.GapState_GAP_STATE_DUE,
	Resolved: stipulatorv1.GapState_GAP_STATE_RESOLVED,
}

// BucketProto maps a bucket to its wire enum, for report composers.
func BucketProto(b Bucket) stipulatorv1.Bucket { return bucketProto[b] }

// GapStateProto maps a gap state to its wire enum, for report composers.
func GapStateProto(s GapState) stipulatorv1.GapState { return gapProto[s] }

// GapStateWord is the one human spelling of a wire gap state: the
// lifecycle states through GapState.String, the dangling class its own
// word — human renderings print the lowercase words (REQ-gap-list).
func GapStateWord(s stipulatorv1.GapState) string {
	if s == stipulatorv1.GapState_GAP_STATE_DANGLING {
		return "dangling"
	}
	for k, v := range gapProto {
		if v == s {
			return k.String()
		}
	}
	return Open.String()
}

// GapListLine is the one account of a gap listing on both faces: the
// row count, the lifecycle tally over the evaluated rows, and the
// dangling rows counted apart — outside the lifecycle, never among the
// contradicted (REQ-gap-list).
func GapListLine(rows []*stipulatorv1.GapReport, dangling int) string {
	return fmt.Sprintf("%d gap records: %s, %d dangling", len(rows), GapCountsWire(rows).Text(), dangling)
}

// RedBucket is Bucket.Red over the wire enum: the same membership for
// composers and renderers that read the report rather than the
// evaluation. An enum value outside the table reads as the zero bucket
// and is therefore red — fail-closed: a bucket the ladder does not know
// surfaces as a violation, never as a pass.
func RedBucket(b stipulatorv1.Bucket) bool {
	return reportBucket(b).Red()
}

// BucketWord is the one human spelling of a wire bucket — Bucket.String
// over the enum; every renderer of a wire bucket reads it (the check
// summary, the digests, the human rows, the context dossier), so the
// word is one on every face.
func BucketWord(b stipulatorv1.Bucket) string {
	return reportBucket(b).String()
}

// Buckets lists the closed bucket set in declaration order — the one
// enumeration a vocabulary derived from the buckets pins itself against.
func Buckets() []Bucket {
	out := make([]Bucket, 0, len(bucketProto))
	for b := range bucketProto {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// reportBucket maps the wire enum back to the report's bucket; an enum value
// outside the table reads as Uncovered, the zero bucket, exactly as
// Bucket.String reads an unknown bucket.
func reportBucket(b stipulatorv1.Bucket) Bucket {
	for k, v := range bucketProto {
		if v == b {
			return k
		}
	}
	return Uncovered
}

// RedFold classifies why a red row is or is not rendered as an ordinary
// red: the row restates a result-level cause, or it stands on its own.
type RedFold int

const (
	// RedVisible: red for a reason of its own — every projection shows it.
	RedVisible RedFold = iota
	// RedPolicyBlocked: red solely on the witness-selection boundary while
	// the result-level diagnostic fired — bounded projections fold it into
	// a count behind that diagnostic (REQ-check-witness-selection).
	RedPolicyBlocked
	// RedScopeBlocked: red solely because the caller's id scope left its
	// witnesses unexecuted on a scoped pass — folded the same way behind
	// the result's partial flag.
	RedScopeBlocked
)

// RedRow is one red requirement of a check result as the one ladder
// classified it: its bucket word, its reasons, and its fold class.
type RedRow struct {
	Id      string
	Bucket  string
	Reasons []string
	Fold    RedFold
}

// RedRows is the one red-row ladder over a check result, read by every
// projection — the summary, the served digest, and the human rendering
// — so a row is red everywhere or nowhere and its fold class is one
// (REQ-gate-no-undeclared). Membership is RedBucket; the fold gates on
// the result-level flags exactly as the folds are specified: a row red
// solely on the witness-selection boundary folds only while the
// result-level diagnostic fired, a scope-boundary row only on a scoped
// pass, the boundary tested first. Each projection applies its own
// bound and rendering to the rows; none re-derives membership.
func RedRows(res *stipulatorv1.CheckResult) []RedRow {
	var rows []RedRow
	for _, r := range res.GetCoverage().GetRequirements() {
		if !RedBucket(r.GetBucket()) {
			continue
		}
		row := RedRow{Id: r.GetId(), Bucket: BucketWord(r.GetBucket()), Reasons: r.GetReasons()}
		switch {
		case res.GetWitnessSelectionProblem() != "" && r.GetWitnessSelectionBlocked():
			row.Fold = RedPolicyBlocked
		case res.GetScopePartial() && r.GetScopeBlocked():
			row.Fold = RedScopeBlocked
		}
		rows = append(rows, row)
	}
	return rows
}
