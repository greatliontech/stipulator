package verifyrun

import (
	"fmt"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/remedy"
)

// GapRows is the one row set of a gap listing, read by both faces: the
// dangling records first — a triage fact, not a refusal; the list is
// where they are found, and a capped list must drop ordinary evaluated
// rows before the rows demanding repair — then every evaluated row
// whose requirement the corpus knows, as the evaluation projected it
// (state, consent, the declared bits). The evaluation's row for an
// out-of-corpus record is a meaningless Open; the dangling row owns
// it. The dangling count rides beside the rows: those rows are outside
// the lifecycle and tally nowhere else (REQ-gap-list).
func GapRows(spec *stipulatorv1.Spec, store *records.Store, cov *coverage.Report) (rows []*stipulatorv1.GapReport, dangling int) {
	known := records.HashesOf(spec)
	for _, gf := range store.Gaps {
		if known.Known(gf.Gap.GetRequirementId()) {
			continue
		}
		manual := gf.Gap.GetLands().GetManual()
		m := &stipulatorv1.GapReport{}
		m.SetPath(gf.Path)
		m.SetRequirementId(gf.Gap.GetRequirementId())
		m.SetState(stipulatorv1.GapState_GAP_STATE_DANGLING)
		m.SetReason(gf.Gap.GetReason())
		m.SetCondition(coverage.ConditionText(gf.Gap.GetLands()))
		m.SetFired(manual.GetFired())
		m.SetContradicted(manual.GetContradicted())
		rows = append(rows, m)
		dangling++
	}
	for _, g := range cov.GapsProto() {
		if !known.Known(g.GetRequirementId()) {
			continue
		}
		rows = append(rows, g)
	}
	return rows, dangling
}

// The states a read surface's caveat names: the gap listing's
// evaluated states, the context tool's dossier states.
const (
	CaveatEvaluatedStates = "evaluated states"
	CaveatDossierStates   = "dossier states"
)

// MisreportCaveat is the one caveat a read surface states over a
// record with verification problems — the listing stands, the named
// states may misreport, and the repair is named in the one executable
// spelling (REQ-gap-list, REQ-change-remediation).
func MisreportCaveat(problems int, what string) string {
	noun := "problems"
	if problems == 1 {
		noun = "problem"
	}
	return fmt.Sprintf("%d verification %s: %s may misreport — run %s", problems, noun, what, remedy.Verify())
}
