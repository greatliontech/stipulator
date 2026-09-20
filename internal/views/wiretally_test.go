package views

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// The check summary's binding counts are the wire report's tally — read,
// never recounted from the rows: a message whose counters and rows
// disagree summarizes by its counters.
func TestCheckSummaryReadsTheWireVerifyTally(t *testing.T) {
	stipulate.Covers(t, "REQ-report-check-result")
	unpinned := &stipulatorv1.BindingResult{}
	unpinned.SetContentPinned(false)
	unpinned.SetResolution(stipulatorv1.Resolution_RESOLUTION_NOT_FOUND)
	unpinned.SetShape(stipulatorv1.ShapeState_SHAPE_STATE_MISMATCH)
	v := &stipulatorv1.VerifyReport{}
	v.SetResults([]*stipulatorv1.BindingResult{unpinned})
	v.SetStale(3)
	v.SetBroken(2)
	v.SetShapeMismatch(5)
	res := &stipulatorv1.CheckResult{}
	res.SetVerify(v)
	view, err := CheckView(res, "summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := view.(*stipulatorv1.CheckSummary)
	if sum.GetBindingsStale() != 3 || sum.GetBindingsBroken() != 2 || sum.GetBindingsShapeMismatch() != 5 {
		t.Fatalf("bindings tally = stale %d broken %d mismatch %d, want the wire counters 3/2/5 (a recount of the one row would give 1/1/1)",
			sum.GetBindingsStale(), sum.GetBindingsBroken(), sum.GetBindingsShapeMismatch())
	}
}

// The one heading names the unit and the disposition, a degraded
// execution distinctly from an assertion failure, a build failure and a
// timeout each by their own word (REQ-check-diagnostics).
func TestDiagnosticHeadingNamesEveryDisposition(t *testing.T) {
	stipulate.Covers(t, "REQ-check-diagnostics")
	cases := map[stipulatorv1.HealthDisposition]string{
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED:     "degraded: example.com/m.TestX",
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED: "build failed: example.com/m.TestX",
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT:      "timeout: example.com/m.TestX",
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED:  "failed: example.com/m.TestX",
	}
	for disp, want := range cases {
		d := &stipulatorv1.FailureDiagnostic{}
		d.SetInvocation("all")
		d.SetPackage("example.com/m")
		d.SetTest("TestX")
		d.SetDisposition(disp)
		if got := DiagnosticHeading(d); got != want {
			t.Errorf("%v: heading = %q, want %q", disp, got, want)
		}
	}
	// Without a test the package is the unit; without either, the
	// invocation.
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetInvocation("all")
	if got := DiagnosticHeading(d); got != "failed: all" {
		t.Errorf("invocation-level heading = %q", got)
	}
	d.SetPackage("example.com/m")
	if got := DiagnosticHeading(d); got != "failed: example.com/m" {
		t.Errorf("package-level heading = %q", got)
	}
}
