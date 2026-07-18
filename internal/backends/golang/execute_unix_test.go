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

	"google.golang.org/protobuf/types/known/durationpb"

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

// normalizedFixtureInvocation normalizes one cache-bypassing invocation
// over a temporary module, pinning the complete child environment the
// way every policy invocation is pinned.
func normalizedFixtureInvocation(t *testing.T, dir, name string, timeout time.Duration) *NormalizedInvocation {
	t.Helper()
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"."})
	cfg.SetCacheMode(stipulatorv1.GoCacheMode_GO_CACHE_MODE_BYPASS)
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName(name)
	inv.SetTimeout(durationpb.New(timeout))
	inv.SetGo(cfg)
	n, err := NormalizeInvocation(context.Background(), dir, inv)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestGoExecuteEnvelopeTimeoutRetainsGoroutineDump pins the envelope
// kill's evidence: expiry escalates through SIGQUIT before SIGKILL, so a
// test binary wedged on a channel — including one wedged before its own
// -test.timeout timer is armed — dies printing a goroutine dump, and the
// TIMEOUT diagnostic retains that dump instead of discarding the cut-off
// process's output.
func TestGoExecuteEnvelopeTimeoutRetainsGoroutineDump(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a real go test to envelope expiry")
	}
	stipulate.Covers(t, "REQ-go-policy-complete")
	neutralAmbient(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/hangdump\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package hangdump\n\n" +
		"import (\n\t\"testing\"\n\t\"time\"\n)\n\n" +
		"// TestWedge blocks on a channel no sender ever fills; the sleeping\n" +
		"// goroutine keeps the runtime's deadlock detector quiet.\n" +
		"func TestWedge(t *testing.T) {\n" +
		"\tch := make(chan struct{})\n" +
		"\tgo func() {\n\t\ttime.Sleep(time.Hour)\n\t\tclose(ch)\n\t}()\n" +
		"\t<-ch\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "hang_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// The invocation is normalized like any policy invocation, so the
	// child environment is fully pinned — a hand-built struct would
	// inherit whatever GOWORK the surrounding runner pinned and resolve
	// the temp module against the wrong workspace.
	n := normalizedFixtureInvocation(t, dir, "hangdump", 2*time.Second)
	selection := []Obligation{{Kind: ObligationPackage, Package: "example.com/hangdump"}}
	health, _, diags, _, err := ExecuteInvocation(context.Background(), n, selection)
	if err != nil {
		t.Fatal(err)
	}
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Fatalf("invocation disposition = %v, want TIMEOUT (diags: %v)", got, diags)
	}
	if len(diags) != 1 {
		t.Fatalf("timeout run retained %d diagnostics, want 1: %v", len(diags), diags)
	}
	if out := diags[0].GetOutput(); !strings.Contains(out, "goroutine ") {
		t.Errorf("timeout diagnostic lost the kill-time goroutine dump:\n%s", out)
	}
}

// TestGoExecuteCallerDeadlineSkipsDumpGrace pins the kill discrimination:
// a caller's own deadline — not the invocation envelope — discards the
// run whole, so the kill is immediate: no SIGQUIT, no grace, an abort
// well inside the dump window even against a wedged binary.
func TestGoExecuteCallerDeadlineSkipsDumpGrace(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a real go test to a caller deadline")
	}
	stipulate.Covers(t, "REQ-policy-cancellation")
	neutralAmbient(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/hangfast\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The binary ignores SIGQUIT: a cooperative child dies on the QUIT
	// itself and would mask a wrongly-paid grace, so only an ignoring
	// child can prove the immediate-kill path.
	src := "package hangfast\n\n" +
		"import (\n\t\"os\"\n\t\"os/signal\"\n\t\"syscall\"\n\t\"testing\"\n\t\"time\"\n)\n\n" +
		"func TestMain(m *testing.M) {\n" +
		"\tsignal.Ignore(syscall.SIGQUIT)\n" +
		"\tos.Exit(m.Run())\n}\n\n" +
		"func TestWedge(t *testing.T) {\n" +
		"\tch := make(chan struct{})\n" +
		"\tgo func() {\n\t\ttime.Sleep(time.Hour)\n\t\tclose(ch)\n\t}()\n" +
		"\t<-ch\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "hang_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	n := normalizedFixtureInvocation(t, dir, "hangfast", time.Hour)
	selection := []Obligation{{Kind: ObligationPackage, Package: "example.com/hangfast"}}
	// The deadline leaves room for the trivial build; the wedge then holds
	// the binary until the caller deadline fires.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Now()
	health, _, _, _, err := ExecuteInvocation(ctx, n, selection)
	elapsed := time.Since(start)
	if err == nil || health != nil {
		t.Fatalf("caller-deadline run returned a report (health=%v err=%v); it must be discarded whole", health, err)
	}
	// Well inside deadline + grace: an immediate kill returns almost at
	// the deadline; paying the 10s SIGQUIT grace would land past it.
	if limit := 15 * time.Second; elapsed > limit {
		t.Errorf("caller-deadline abort took %v, want < %v (the dump grace must not be paid)", elapsed, limit)
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
