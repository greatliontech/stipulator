package mcpserver

import (
	"reflect"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// The digest's red rows come from the one ladder: a policy-blocked row
// folds to the count behind the result-level diagnostic, a scope-blocked
// row leaves the digest (checkLine carries its count), a red of its own
// renders as id [bucket]: reason — and with no result-level cause raised
// every red is a row.
func TestCheckDigestRowsFoldThroughTheLadder(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	row := func(id string, policy, scope bool) *stipulatorv1.RequirementCoverage {
		r := &stipulatorv1.RequirementCoverage{}
		r.SetId(id)
		r.SetBucket(stipulatorv1.Bucket_BUCKET_BROKEN)
		r.SetReasons([]string{id + " reason"})
		r.SetWitnessSelectionBlocked(policy)
		r.SetScopeBlocked(scope)
		return r
	}
	cov := &stipulatorv1.CoverageReport{}
	cov.SetRequirements([]*stipulatorv1.RequirementCoverage{row("REQ-policy", true, false), row("REQ-scope", false, true), row("REQ-plain", false, false)})
	res := &stipulatorv1.CheckResult{}
	res.SetCoverage(cov)
	res.SetWitnessSelectionProblem("no expected witness")
	res.SetScopePartial(true)
	rows, blocked := checkDigestRows(res)
	if want := []string{"REQ-plain [broken]: REQ-plain reason"}; !reflect.DeepEqual(rows, want) || blocked != 1 {
		t.Fatalf("digest rows = %q, blocked = %d; want %q and 1", rows, blocked, want)
	}
	res.SetWitnessSelectionProblem("")
	res.SetScopePartial(false)
	rows, blocked = checkDigestRows(res)
	if len(rows) != 3 || blocked != 0 {
		t.Fatalf("with no result-level cause every red is a row: %q, blocked %d", rows, blocked)
	}
}
