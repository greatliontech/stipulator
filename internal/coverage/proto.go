package coverage

import (
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
		reqs = append(reqs, m)
	}
	out.SetRequirements(reqs)
	out.SetPolicyOverrides(r.PolicyOverrides)

	var gaps []*stipulatorv1.GapReport
	for _, g := range r.Gaps {
		m := &stipulatorv1.GapReport{}
		m.SetPath(g.Path)
		m.SetRequirementId(g.RequirementId)
		m.SetState(gapProto[g.State])
		gaps = append(gaps, m)
	}
	out.SetGaps(gaps)
	out.SetViolations(r.Violations)
	out.SetGatePasses(r.GatePasses())
	return out
}

var bucketProto = map[Bucket]stipulatorv1.Bucket{
	Uncovered: stipulatorv1.Bucket_BUCKET_UNCOVERED,
	Stale:     stipulatorv1.Bucket_BUCKET_STALE,
	Broken:    stipulatorv1.Bucket_BUCKET_BROKEN,
	Covered:   stipulatorv1.Bucket_BUCKET_COVERED,
	Exempt:    stipulatorv1.Bucket_BUCKET_EXEMPT,
}

var gapProto = map[GapState]stipulatorv1.GapState{
	Open:     stipulatorv1.GapState_GAP_STATE_OPEN,
	Due:      stipulatorv1.GapState_GAP_STATE_DUE,
	Resolved: stipulatorv1.GapState_GAP_STATE_RESOLVED,
}
