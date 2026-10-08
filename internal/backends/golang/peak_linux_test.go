//go:build linux

package golang

import (
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/resident"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestCompletedPackagePeakFeedsTheEstimate pins the completed-package
// evidence of the admission's memory term
// (REQ-evidence-witness-freshness): a reaped process's wait status yields
// its tree's largest resident set in bytes, and the admission's estimate
// after its release is at least that peak, never below the floor.
func TestCompletedPackagePeakFeedsTheEstimate(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	peak := processPeakBytes(cmd.ProcessState)
	if peak < 1<<20 || peak%1024 != 0 {
		t.Fatalf("processPeakBytes of a reaped shell = %d, want at least a mebibyte in bytes (the kernel's kibibytes scaled), a kibibyte multiple", peak)
	}
	if got := processPeakBytes(nil); got != 0 {
		t.Fatalf("processPeakBytes(nil) = %d, want 0", got)
	}
	freshGate(t)
	a := newAdmission(t.Context(), 1)
	if admitted, _, _ := a.admit(); !admitted {
		t.Fatal("the one slot was not admitted")
	}
	a.reaped("", 0, peak)
	a.release()
	if got, _ := a.estimate(resident.Reading{}); got != max(packageEstimateFloor, peak) {
		t.Fatalf("estimate after a completed peak of %d = %d, want max(floor, peak) = %d", peak, got, max(packageEstimateFloor, peak))
	}
}

// TestInvocationReleasesEachPackageWithItsPeak pins the wiring of the
// completed-package evidence and of the tree attribution
// (REQ-evidence-witness-freshness, REQ-evidence-admission-origin):
// every package an invocation runs is reaped to the gate with the peak
// its process reported — a positive byte count, never zero — and with
// the process the executor registered for it at the spawn, the root of
// the tree the reservation attributed.
func TestInvocationReleasesEachPackageWithItsPeak(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the tree")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-evidence-admission-origin")
	neutralAmbient(t)
	var mu sync.Mutex
	released := map[string]uint64{}
	registered := map[string]bool{}
	prior := reapedPeakHook
	reapedPeakHook = func(pkg string, pid int, wasRegistered bool, peakBytes uint64) {
		mu.Lock()
		defer mu.Unlock()
		released[pkg] = peakBytes
		registered[pkg] = wasRegistered && pid > 0
	}
	t.Cleanup(func() { reapedPeakHook = prior })
	// The invocation's gate is followed through the seam to its drop.
	var gates []*admission
	priorObserver := admissionObserverForTest
	admissionObserverForTest = func(a *admission) {
		mu.Lock()
		defer mu.Unlock()
		gates = append(gates, a)
	}
	t.Cleanup(func() { admissionObserverForTest = priorObserver })
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./ok", "./notest"})
	health, _, diags := executeInvocation(t, time.Minute, cfg, "peaks")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("invocation disposition = %v, want HEALTHY (diags: %v)", got, diags)
	}
	mu.Lock()
	defer mu.Unlock()
	// The invocation's admission left the host gate with it: no
	// package of this operation is judged against after the run.
	if len(gates) == 0 {
		t.Fatal("the invocation minted no admission")
	}
	for _, g := range gates {
		if theHostGate.member(g) {
			t.Fatal("the invocation's admission still sits on the host gate after the run returned")
		}
	}
	for _, pkg := range []string{"example.com/exec/ok", "example.com/exec/notest"} {
		if peak, ok := released[pkg]; !ok || peak < 1<<20 {
			t.Fatalf("package %s released with peak %d (present %v), want its reaped process's peak of at least a mebibyte; released: %v", pkg, peak, ok, released)
		}
		if !registered[pkg] {
			t.Fatalf("package %s was released without the process the executor spawned for it registered on the gate: %v", pkg, registered)
		}
	}
}
