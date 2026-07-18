//go:build unix

package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestGoExecuteEnvelopeTimeoutNamesAbortedTests pins the timeout
// diagnostic's residue: when the envelope expires over a run that had
// started tests, the TIMEOUT diagnostic names the started-but-unfinished
// tests instead of reporting bare expiry. A hanging toolchain stand-in
// makes the cutoff deterministic.
func TestGoExecuteEnvelopeTimeoutNamesAbortedTests(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	bin := t.TempDir()
	stub := filepath.Join(bin, "go")
	script := "#!/bin/sh\n" +
		// The child environment strips PATH down to the stub directory;
		// restore the standard locations inside the script so its own
		// tools resolve.
		"PATH=/usr/bin:/bin\n" +
		`echo '{"Action":"start","Package":"example.com/hang"}'` + "\n" +
		`echo '{"Action":"run","Package":"example.com/hang","Test":"TestHang"}'` + "\n" +
		"sleep 60\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	n := &NormalizedInvocation{
		Name:     "hang",
		Dir:      t.TempDir(),
		Packages: []string{"./..."},
		Timeout:  700 * time.Millisecond,
		Env:      []string{"PATH=" + bin},
	}
	selection := []Obligation{{Kind: ObligationPackage, Package: "example.com/hang"}}
	health, _, diags, observations, err := ExecuteInvocation(context.Background(), n, selection)
	if err != nil {
		t.Fatal(err)
	}
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Fatalf("invocation disposition = %v, want TIMEOUT", got)
	}
	if len(diags) != 1 || !strings.Contains(diags[0].GetOutput(), "started but unfinished: TestHang") {
		t.Errorf("timeout diagnostic does not name the aborted test: %v", diags)
	}
	// The launched, cut-off process still owns its observation — an
	// incomplete one bound to the real process, never a completed record
	// and never silence.
	if len(observations) != 1 {
		t.Fatalf("timeout run owns %d observations, want 1", len(observations))
	}
	o := observations[0].Wire
	if o.GetCompleted() != nil || o.GetIncompleteReason() == "" {
		t.Errorf("cut-off process observation = %v, want incomplete with a reason", o)
	}
	if o.GetProducer().GetProcessId() <= 0 || o.GetProducer().GetInvocation() != "hang" {
		t.Errorf("observation producer = %v, want the launched process bound", o.GetProducer())
	}
}

// TestGoExecuteSilentToolchainDegradesEndToEnd drives the whole spawn
// path against a toolchain stand-in that exits zero without a single
// event: the package must dispose DEGRADED — a silent command stream is
// refused even when the process claims success.
func TestGoExecuteSilentToolchainDegradesEndToEnd(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	bin := t.TempDir()
	stub := filepath.Join(bin, "go")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	n := &NormalizedInvocation{
		Name:     "stub",
		Dir:      t.TempDir(),
		Packages: []string{"./..."},
		Timeout:  30 * time.Second,
		Env:      []string{"PATH=" + bin},
	}
	selection := []Obligation{{Kind: ObligationPackage, Package: "example.com/silent"}}
	health, tests, diags, _, err := ExecuteInvocation(context.Background(), n, selection)
	if err != nil {
		t.Fatal(err)
	}
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
		t.Fatalf("invocation disposition = %v, want DEGRADED for a silent toolchain", got)
	}
	if got := len(health.GetPackages()); got != 1 || health.GetPackages()[0].GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
		t.Errorf("package dispositions = %v, want the one selected package DEGRADED", health.GetPackages())
	}
	if len(tests) != 0 {
		t.Errorf("a silent stream produced outcomes: %v", tests)
	}
	if len(diags) != 1 {
		t.Errorf("silent degradation retained %d diagnostics, want 1", len(diags))
	}
}

// TestGoExecuteReportsPerInvocationProgress pins the executor's leg of
// the progress seam: with a reporter installed, each selected package's
// completion is counted against the invocation, and the final package
// always emits the invocation-completion milestone with its counts.
func TestGoExecuteReportsPerInvocationProgress(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	bin := t.TempDir()
	stub := filepath.Join(bin, "go")
	script := "#!/bin/sh\n" +
		"pkg=\"\"\nfor a in \"$@\"; do pkg=\"$a\"; done\n" +
		"printf '{\"Action\":\"pass\",\"Package\":\"'\"$pkg\"'\"}\\n'\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	n := &NormalizedInvocation{
		Name:     "steps",
		Dir:      t.TempDir(),
		Packages: []string{"./..."},
		Timeout:  30 * time.Second,
		Env:      []string{"PATH=" + bin},
	}
	selection := []Obligation{
		{Kind: ObligationPackage, Package: "example.com/a"},
		{Kind: ObligationPackage, Package: "example.com/b"},
	}
	var (
		mu     sync.Mutex
		events []*stipulatorv1.ProgressEvent
	)
	rep := progress.New(func(e *stipulatorv1.ProgressEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}, progress.WithInterval(time.Hour))
	ctx := progress.NewContext(context.Background(), rep)
	if _, _, _, _, err := ExecuteInvocation(ctx, n, selection); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("execution reported no per-invocation progress")
	}
	final := events[len(events)-1]
	if final.GetInvocation() != "steps" || final.GetCompleted() != 2 || final.GetTotal() != 2 {
		t.Fatalf("completion milestone = %v, want steps 2/2", final)
	}
}
