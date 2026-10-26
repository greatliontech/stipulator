package views

import (
	"fmt"
	"testing"

	pb "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/proto"
)

func loadAccount(rows int) *pb.WitnessCacheLoadAccount {
	a := &pb.WitnessCacheLoadAccount{}
	a.SetOmitted(7)
	a.SetIncomplete(true)
	a.SetProblem("listing interrupted")
	a.SetProblemTruncated(true)
	for i := range rows {
		r := &pb.WitnessCacheRefusal{}
		r.SetFile(fmt.Sprintf("variant-%02d", i))
		r.SetDetail("rejected envelope")
		r.SetDetailTruncated(true)
		r.SetFilenameTruncated(true)
		r.SetCoordinateOmitted(true)
		a.SetRefusals(append(a.GetRefusals(), r))
	}
	return a
}

func TestWitnessCacheSummaryConservesObservationsAndOwnership(t *testing.T) {
	t.Parallel()
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals", "REQ-mcp-views-check-summary")
	for n := 0; n <= 25; n++ {
		a := loadAccount(n)
		before := proto.CloneOf(a)
		summary := WitnessCacheLoadSummary(a)
		if len(summary.GetRefusals()) != min(n, 5) || uint64(len(summary.GetRefusals()))+summary.GetOmitted() != uint64(n)+7 {
			t.Fatalf("%d rows: lossy count: %v", n, summary)
		}
		if !proto.Equal(summary, WitnessCacheLoadSummary(summary)) || !proto.Equal(a, before) {
			t.Fatal("summary is not idempotent or mutates its source")
		}
		if !summary.GetIncomplete() || summary.GetProblem() != a.GetProblem() || !summary.GetProblemTruncated() {
			t.Fatal("partial account metadata lost")
		}
		for i, r := range summary.GetRefusals() {
			if !proto.Equal(r, a.GetRefusals()[i]) {
				t.Fatal("row order or flags changed")
			}
			r.SetFile("changed")
		}
		if !proto.Equal(a, before) {
			t.Fatal("summary aliases source rows")
		}
	}
	if WitnessCacheLoadSummary(nil) != nil {
		t.Fatal("invented an account")
	}
}

func TestWitnessCacheAccountSurvivesEmptyScopesWithoutChangingVerdicts(t *testing.T) {
	t.Parallel()
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals")
	a := loadAccount(25)
	store := &records.Store{}
	vr := verify.Correlate(&verify.Report{}, store, &verify.TestRun{WitnessCacheLoad: a})
	cov := coverage.Evaluate(&pb.Spec{}, vr, store, true, nil)
	if !proto.Equal(vr.Proto().GetWitnessCacheLoad(), a) || !proto.Equal(cov.Proto().GetWitnessCacheLoad(), a) {
		t.Fatal("correlation or coverage dropped the load account")
	}
	baseline := verify.Correlate(&verify.Report{}, store, &verify.TestRun{})
	baseCov := coverage.Evaluate(&pb.Spec{}, baseline, store, true, nil)
	vwire, cwire := vr.Proto(), cov.Proto()
	vwire.ClearWitnessCacheLoad()
	cwire.ClearWitnessCacheLoad()
	if !proto.Equal(vwire, baseline.Proto()) || !proto.Equal(cwire, baseCov.Proto()) || cov.GatePasses() != baseCov.GatePasses() {
		t.Fatal("advisory account changed evidence or verdict")
	}
	for _, scope := range []Scope{{}, {Ids: []string{"absent"}}, {Path: "absent"}, {Filter: "absent*"}} {
		for _, view := range []string{"summary", "bindings"} {
			m, err := VerifyView(vr, Facts{}, view, scope)
			if err != nil {
				t.Fatal(err)
			}
			got := m.(interface {
				GetWitnessCacheLoad() *pb.WitnessCacheLoadAccount
			}).GetWitnessCacheLoad()
			want := a
			if view == "summary" {
				want = WitnessCacheLoadSummary(a)
			}
			if !proto.Equal(got, want) {
				t.Fatalf("verify %s scope %v lost global account", view, scope)
			}
		}
		for _, view := range []string{"summary", "reds", "full"} {
			m, err := CoverageView(cov, Facts{}, view, scope)
			if err != nil {
				t.Fatal(err)
			}
			got := m.(interface {
				GetWitnessCacheLoad() *pb.WitnessCacheLoadAccount
			}).GetWitnessCacheLoad()
			want := a
			if view == "summary" {
				want = WitnessCacheLoadSummary(a)
			}
			if !proto.Equal(got, want) {
				t.Fatalf("coverage %s scope %v lost global account", view, scope)
			}
		}
	}
	res := &pb.CheckResult{}
	res.SetWitnessCacheLoad(a)
	res.SetCoverage(cwire)
	res.SetVerify(vwire)
	for _, view := range []string{"summary", "full"} {
		m, err := CheckView(res, view, []string{"absent"})
		if err != nil {
			t.Fatal(err)
		}
		got := m.(interface {
			GetWitnessCacheLoad() *pb.WitnessCacheLoadAccount
		}).GetWitnessCacheLoad()
		want := a
		if view == "summary" {
			want = WitnessCacheLoadSummary(a)
		}
		if !proto.Equal(got, want) {
			t.Fatalf("check %s lost global account", view)
		}
	}
}
