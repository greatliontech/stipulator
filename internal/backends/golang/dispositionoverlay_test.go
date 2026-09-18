package golang

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// A later pass folds into the first: its rows join, and its package
// dispositions overlay the first pass's per package with the worse of
// the two standing: a healthy retry over the
// drifted subjects alone never erases the first pass's timeout that
// denied the rest their outcome, and a retry timeout is the cause of
// what it denied; a package only the later pass ran carries the later
// pass's disposition (REQ-check-witness-selection).
func TestRetryDispositionsOverlayByTheWorse(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	const (
		healthy = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY
		timeout = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT
		failed  = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED
	)
	first := newExecMerge()
	first.pkgDisp[invPkgKey("race", "p")] = timeout
	first.pkgDisp[invPkgKey("race", "q")] = healthy
	first.pkgDisp[invPkgKey("race", "r")] = failed
	retry := newExecMerge()
	retry.pkgDisp[invPkgKey("race", "p")] = healthy
	retry.pkgDisp[invPkgKey("race", "q")] = timeout
	retry.pkgDisp[invPkgKey("race", "r")] = healthy
	retry.pkgDisp[invPkgKey("race", "s")] = failed
	retry.rows = append(retry.rows, &stipulatorv1.TestResult{})
	first.absorb(retry)
	if len(first.rows) != 1 {
		t.Fatalf("absorb appended %d rows, want the retry's one", len(first.rows))
	}
	want := map[string]stipulatorv1.HealthDisposition{
		invPkgKey("race", "p"): timeout,
		invPkgKey("race", "q"): timeout,
		invPkgKey("race", "r"): failed,
		invPkgKey("race", "s"): failed,
	}
	for key, disposition := range want {
		if got := first.pkgDisp[key]; got != disposition {
			t.Errorf("%q after the overlay = %v, want %v", key, got, disposition)
		}
	}
	if cause, ok := first.packageCause("race", "p"); !ok || cause != dispositionCause("race", "p", timeout) {
		t.Fatalf("p's cause after a healthy retry = %q, %v; want the first pass's timeout", cause, ok)
	}
}
