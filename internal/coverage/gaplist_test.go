package coverage

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// The listing's words and its one account line, as literals: the
// lifecycle words, the dangling class its own word, the line's count,
// tally, and dangling apart.
func TestGapListWordsAndLine(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-list")
	words := map[stipulatorv1.GapState]string{
		stipulatorv1.GapState_GAP_STATE_OPEN:     "open",
		stipulatorv1.GapState_GAP_STATE_DUE:      "due",
		stipulatorv1.GapState_GAP_STATE_RESOLVED: "resolved",
		stipulatorv1.GapState_GAP_STATE_DANGLING: "dangling",
	}
	// Every lifecycle state has its word here: a state added to the
	// ladder without a row is loud.
	if len(words)-1 != len(gapProto) {
		t.Fatalf("the table names %d lifecycle states, the ladder %d", len(words)-1, len(gapProto))
	}
	for s, want := range words {
		if got := GapStateWord(s); got != want {
			t.Errorf("GapStateWord(%v) = %q, want %q", s, got, want)
		}
	}
	row := func(s stipulatorv1.GapState, contradicted bool) *stipulatorv1.GapReport {
		m := &stipulatorv1.GapReport{}
		m.SetState(s)
		m.SetContradicted(contradicted)
		return m
	}
	rows := []*stipulatorv1.GapReport{
		row(stipulatorv1.GapState_GAP_STATE_DANGLING, true),
		row(stipulatorv1.GapState_GAP_STATE_OPEN, true), row(stipulatorv1.GapState_GAP_STATE_OPEN, false), row(stipulatorv1.GapState_GAP_STATE_OPEN, false),
		row(stipulatorv1.GapState_GAP_STATE_DUE, true), row(stipulatorv1.GapState_GAP_STATE_DUE, false),
		row(stipulatorv1.GapState_GAP_STATE_RESOLVED, true),
	}
	if got := GapListLine(rows, 1); got != "7 gap records: 3 open, 2 due, 1 resolved (2 of the unresolved contradicted), 1 dangling" {
		t.Fatalf("line = %q", got)
	}
}
