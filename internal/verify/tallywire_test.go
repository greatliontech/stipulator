package verify

import (
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// The wire report carries the report's own tally, projected once: a
// reader of the message takes the counts the report derived, so no
// summary re-derives them from the rows.
func TestReportProtoCarriesTheTally(t *testing.T) {
	stipulate.Covers(t, "REQ-report-check-result")
	rep := &Report{Results: []BindingResult{
		{ContentPinned: false, Resolution: NotFound},
		{ContentPinned: false, Resolution: Resolved, Shape: ShapeMismatch},
		{ContentPinned: true, Resolution: NotFound},
	}}
	rep.Tally()
	if rep.Stale != 2 || rep.Broken != 2 || rep.ShapeMismatch != 1 {
		t.Fatalf("tally = stale %d broken %d mismatch %d, want 2/2/1", rep.Stale, rep.Broken, rep.ShapeMismatch)
	}
	m := rep.Proto()
	if m.GetStale() != 2 || m.GetBroken() != 2 || m.GetShapeMismatch() != 1 {
		t.Fatalf("wire tally = stale %d broken %d mismatch %d, want the report's 2/2/1", m.GetStale(), m.GetBroken(), m.GetShapeMismatch())
	}
	// The projection reads the counters, never the rows: a report whose
	// counters differ from its rows carries its counters.
	rep.Stale, rep.Broken, rep.ShapeMismatch = 7, 8, 9
	m = rep.Proto()
	if m.GetStale() != 7 || m.GetBroken() != 8 || m.GetShapeMismatch() != 9 {
		t.Fatalf("wire tally = %d/%d/%d, want the counters 7/8/9", m.GetStale(), m.GetBroken(), m.GetShapeMismatch())
	}
}
