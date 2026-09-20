package verifyrun

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
)

// The listing's rows are one set on both faces: the dangling records
// first, then the evaluated rows the corpus knows — with their consent
// state — and never the evaluation's row for an out-of-corpus record;
// the dangling count rides beside them. The caveat over a problem-
// bearing record names the repair in the one executable spelling.
func TestGapRowsIsTheOneListing(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-list")
	spec := &stipulatorv1.Spec{}
	known := &stipulatorv1.Requirement{}
	known.SetId("REQ-a")
	known.SetText("It MUST a.")
	spec.SetRequirements([]*stipulatorv1.Requirement{known})

	gap := func(id string, fired, contradicted bool) records.GapFile {
		g := &stipulatorv1.Gap{}
		g.SetRequirementId(id)
		g.SetReason("why " + id)
		m := &stipulatorv1.ManualCondition{}
		m.SetCondition("the work lands")
		m.SetFired(fired)
		m.SetContradicted(contradicted)
		l := &stipulatorv1.LandingCondition{}
		l.SetManual(m)
		g.SetLands(l)
		return records.GapFile{Path: ".stipulator/gaps/" + id + ".textproto", Gap: g}
	}
	store := &records.Store{Gaps: []records.GapFile{gap("REQ-a", false, false), gap("REQ-ghost", true, true)}}
	cov := &coverage.Report{Gaps: []coverage.Gap{
		{RequirementId: "REQ-ghost", State: coverage.Open, Reason: "why REQ-ghost"},
		{RequirementId: "REQ-a", State: coverage.Due, StaleConsent: true, Reason: "why REQ-a", Condition: "manual: the work lands"},
	}}

	rows, dangling := GapRows(spec, store, cov)
	if dangling != 1 || len(rows) != 2 {
		t.Fatalf("rows = %d, dangling = %d; want 2 rows, 1 dangling", len(rows), dangling)
	}
	ghost, evaluated := rows[0], rows[1]
	if ghost.GetRequirementId() != "REQ-ghost" || ghost.GetState() != stipulatorv1.GapState_GAP_STATE_DANGLING ||
		!ghost.GetFired() || !ghost.GetContradicted() || ghost.GetCondition() != "manual: the work lands" || ghost.GetPath() == "" {
		t.Fatalf("the dangling row leads and carries its declaration: %v", ghost)
	}
	if evaluated.GetRequirementId() != "REQ-a" || evaluated.GetState() != stipulatorv1.GapState_GAP_STATE_DUE || !evaluated.GetStaleConsent() {
		t.Fatalf("the evaluated row carries the evaluation's state and consent: %v", evaluated)
	}

	if got := MisreportCaveat(2, CaveatEvaluatedStates); got != "2 verification problems: evaluated states may misreport — run stipulator verify" {
		t.Fatalf("caveat = %q", got)
	}
	// The named states are the caller's: the context tool's dossiers.
	if got := MisreportCaveat(1, CaveatDossierStates); got != "1 verification problem: dossier states may misreport — run stipulator verify" {
		t.Fatalf("dossier caveat = %q", got)
	}
}
