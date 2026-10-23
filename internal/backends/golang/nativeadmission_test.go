package golang

import (
	"testing"

	"github.com/greatliontech/gofresh/resident"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestRetainedStagesReserveTheirLiveProcess(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-concurrency", "REQ-evidence-admission-origin")
	a := newAdmission(t.Context(), 2)
	defer a.leave()
	a.running = 1
	a.spawned("package", 101)
	need := packageEstimateFloor
	price, origin := a.estimate(resident.Reading{Trees: map[int]uint64{101: need * 2}})
	if price != need*2 || origin.pid != 101 {
		t.Fatalf("compilation reading lost: %d %+v", price, origin)
	}
	a.reaped("package", 101, need/4)
	a.spawned("package", 102)
	reading := resident.Reading{Trees: map[int]uint64{102: need / 4}}
	price, origin = a.estimate(reading)
	if price != need || origin.term != originFloor {
		t.Fatalf("the compiler's live reading survived its completed wait evidence: %d %+v", price, origin)
	}
	if got := a.reservation(reading, need); got != need*3/4 {
		t.Fatalf("binary reservation=%d", got)
	}
	a.reaped("package", 102, need/2)
	if len(a.pids) != 0 || len(a.treePeak) != 0 || a.peak != need/2 {
		t.Fatalf("stage accounting did not close: %+v", a)
	}
}
