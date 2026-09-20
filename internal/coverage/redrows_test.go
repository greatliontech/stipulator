package coverage

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// The red-row ladder is one: membership is Bucket.Red over every wire
// bucket, the word is Bucket.String, and the fold class gates on the
// result-level flags — the witness-selection boundary first, the scope
// boundary second, a row carrying a boundary flag while the result
// never raised it stays a visible red.
func TestRedRowsIsTheOneLadder(t *testing.T) {
	stipulate.Covers(t, "REQ-gate-no-undeclared")
	// The words and the red membership as literals — the pin never reads
	// them from the ladder it pins.
	words := map[stipulatorv1.Bucket]struct {
		word string
		red  bool
	}{
		stipulatorv1.Bucket_BUCKET_UNCOVERED: {"uncovered", true},
		stipulatorv1.Bucket_BUCKET_PARTIAL:   {"partial", true},
		stipulatorv1.Bucket_BUCKET_STALE:     {"stale", true},
		stipulatorv1.Bucket_BUCKET_BROKEN:    {"broken", true},
		stipulatorv1.Bucket_BUCKET_COVERED:   {"covered", false},
		stipulatorv1.Bucket_BUCKET_EXEMPT:    {"exempt", false},
		stipulatorv1.Bucket_BUCKET_ATTESTED:  {"attested", false},
	}
	if len(words) != len(bucketProto) {
		t.Fatalf("the table names %d buckets, the ladder %d", len(words), len(bucketProto))
	}
	for wire, want := range words {
		if got := RedBucket(wire); got != want.red {
			t.Errorf("RedBucket(%v) = %v, want %v", wire, got, want.red)
		}
		if got := BucketWord(wire); got != want.word {
			t.Errorf("BucketWord(%v) = %q, want %q", wire, got, want.word)
		}
	}
	// Outside the table: the zero bucket's word, and red — fail-closed.
	for _, unknown := range []stipulatorv1.Bucket{stipulatorv1.Bucket_BUCKET_UNSPECIFIED, stipulatorv1.Bucket(99)} {
		if got := BucketWord(unknown); got != "uncovered" {
			t.Errorf("BucketWord(%v) = %q, want uncovered", unknown, got)
		}
		if !RedBucket(unknown) {
			t.Errorf("RedBucket(%v) = false, want red (fail-closed)", unknown)
		}
	}

	row := func(id string, b stipulatorv1.Bucket, policy, scope bool) *stipulatorv1.RequirementCoverage {
		r := &stipulatorv1.RequirementCoverage{}
		r.SetId(id)
		r.SetBucket(b)
		r.SetReasons([]string{id + " reason"})
		r.SetWitnessSelectionBlocked(policy)
		r.SetScopeBlocked(scope)
		return r
	}
	cov := &stipulatorv1.CoverageReport{}
	cov.SetRequirements([]*stipulatorv1.RequirementCoverage{
		row("REQ-both", stipulatorv1.Bucket_BUCKET_BROKEN, true, true),
		row("REQ-scope", stipulatorv1.Bucket_BUCKET_BROKEN, false, true),
		row("REQ-plain", stipulatorv1.Bucket_BUCKET_PARTIAL, false, false),
		row("REQ-green", stipulatorv1.Bucket_BUCKET_ATTESTED, true, true),
	})
	res := &stipulatorv1.CheckResult{}
	res.SetCoverage(cov)

	// Neither result-level cause raised: every red is visible.
	want := map[string]RedFold{"REQ-both": RedVisible, "REQ-scope": RedVisible, "REQ-plain": RedVisible}
	assertFolds(t, RedRows(res), want, "no result-level cause")

	res.SetWitnessSelectionProblem("no expected witness")
	res.SetScopePartial(true)
	want = map[string]RedFold{"REQ-both": RedPolicyBlocked, "REQ-scope": RedScopeBlocked, "REQ-plain": RedVisible}
	rows := RedRows(res)
	assertFolds(t, rows, want, "both causes raised")
	if rows[0].Bucket != "broken" || rows[2].Bucket != "partial" || rows[2].Reasons[0] != "REQ-plain reason" {
		t.Fatalf("rows carry the word and the reasons: %+v", rows)
	}
}

func assertFolds(t *testing.T, rows []RedRow, want map[string]RedFold, when string) {
	t.Helper()
	got := map[string]RedFold{}
	for _, r := range rows {
		got[r.Id] = r.Fold
	}
	if len(got) != len(want) {
		t.Fatalf("%s: rows %v, want %v", when, got, want)
	}
	for id, f := range want {
		if g, ok := got[id]; !ok || g != f {
			t.Errorf("%s: %s fold = %v (present %v), want %v", when, id, g, ok, f)
		}
	}
}
