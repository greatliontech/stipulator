//go:build unix

package golang

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// A cancelled probe returns within its wait bound even when a shim on
// PATH left a descendant holding the output pipe: the kill reaches
// the process alone, and the bounded wait keeps the cancellation live.
func TestCancelledProbeReturnsWithinItsWaitBound(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-cancellation")
	bin := t.TempDir()
	shim := filepath.Join(bin, "go")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n/bin/sleep 8 &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := probeRunner.SampleGoVersion(ctx, ".", normalizedOSEnv(t))
	if err == nil {
		t.Fatal("a cancelled probe reported a version")
	}
	if took := time.Since(start); took > probeWaitDelay+2*time.Second {
		t.Fatalf("cancelled probe held its caller %v; want within the wait bound", took)
	}
}

// A wrapper that answers, exits, and leaves a helper holding the pipe
// has sampled: the answer — the first line, whatever the helper writes
// after it — is taken after the bounded wait, never refused (and never
// memoized as a refusal) over the wrapper's housekeeping.
func TestProbeTakesTheAnswerAWrapperLeftBehind(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\necho go1.27.0\n( /bin/sleep 1; echo helper-done; /bin/sleep 4 ) &\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	v, err := probeRunner.SampleGoVersion(context.Background(), ".", normalizedOSEnv(t))
	if err != nil || v != "go1.27.0" {
		t.Fatalf("sample behind a lingering helper = %q, %v; want the wrapper's answer", v, err)
	}
	if took := time.Since(start); took > probeWaitDelay+2*time.Second {
		t.Fatalf("sample held its caller %v; want within the wait bound", took)
	}
}

// normalizedOSEnv is the process environment under the policy's
// normalization, taken after the test's PATH is set.
func normalizedOSEnv(t *testing.T) []string {
	t.Helper()
	env, err := gotool.NormalizeEnv(os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// The owned runner carries the boundary every go child but the
// loader's runs under (REQ-go-owned-processes, REQ-policy-cancellation):
// the child leads its own process group, the cancellation hook is
// installed with the policy's wait delay, and the quit grace applies to
// the envelope's expiry alone — every other cancellation kills outright.
func TestOwnedRunnerCarriesTheBoundary(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes", "REQ-policy-cancellation")
	env, err := gotool.NormalizeEnv(os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := ownedRunner.Command(context.Background(), ".", env, "env", "GOOS")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid || cmd.Cancel == nil || cmd.WaitDelay != gotool.DefaultWaitDelay {
		t.Fatalf("owned command = attr %+v, cancel %v, wait delay %s; want its own group under the boundary's hook and delay", cmd.SysProcAttr, cmd.Cancel != nil, cmd.WaitDelay)
	}
	c := ownedRunner.Containment
	if c.Grace != quitGrace || !c.Quit(errEnvelopeExpired) || c.Quit(context.Canceled) || c.Quit(nil) {
		t.Fatalf("containment %+v: quit(envelope)=%v quit(cancel)=%v; want the grace on the envelope's expiry alone", c, c.Quit(errEnvelopeExpired), c.Quit(context.Canceled))
	}
}

// The analysis engines' own go commands ride the owned boundary
// (REQ-go-owned-processes): an engine the backend constructs spawns
// through engineRunner — its commands lead their own process group.
func TestAnalysisEnginesRideTheOwnedBoundary(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if testing.Short() {
		t.Skip("constructs an engine over the fixture")
	}
	// The hook may fire from an engine's analysis goroutines: the
	// counters are atomic, and the seam is swapped only while no engine
	// is live (the discipline engineDiagnostics states).
	var grouped, seen atomic.Int32
	engineCommandHook = func(cmd *exec.Cmd) {
		seen.Add(1)
		if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
			grouped.Add(1)
		}
	}
	t.Cleanup(func() { engineCommandHook = nil })
	dir := discoverFixture(t)
	env, err := gotool.NormalizeEnv(os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newEngine(context.Background(), dir, env, nil); err != nil {
		t.Fatal(err)
	}
	if seen.Load() == 0 || grouped.Load() != seen.Load() {
		t.Fatalf("engine commands through the boundary: %d of %d grouped", grouped.Load(), seen.Load())
	}
}

// A wrapper's pipe hold (REQ-go-owned-processes): a go wrapper that
// runs the real toolchain and leaves a descendant holding its pipes
// exits on its own. The witness invocation reads its stream to the end
// through its own pipe, so a hold on stdout only delays the stream's
// end, and a hold on stderr alone — the one pipe the wait bounds — ends
// at the wait delay, truncating residue, never the verdict: the package
// stays healthy and its outcomes stand under either hold. A listing is
// collected by the boundary and has no wholeness test: the hold refuses
// it naming the hold.
func TestWitnessStreamServesAndListingRefusesBehindAPipeHold(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the toolchain under a wrapper")
	}
	for _, hold := range []struct{ name, redirect string }{{"both pipes", ""}, {"stderr alone", " >/dev/null"}} {
		t.Run(hold.name, func(t *testing.T) { witnessAndListingBehindAHold(t, hold.redirect) })
	}
}

func witnessAndListingBehindAHold(t *testing.T, redirect string) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\ntest|list) \"" + goBinary + "\" \"$@\"; s=$?; ( sleep 3 )" + redirect + " & exit $s;;\n*) exec \"" + goBinary + "\" \"$@\";;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	neutralAmbient(t)
	dir := discoverFixture(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("hold", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := listPackages(context.Background(), n); err == nil || !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("a listing behind a pipe hold = %v, want the hold named", err)
	}
	selection := []Obligation{{Kind: ObligationPackage, Package: "example.com/disc/alpha"}}
	health, tests, _, _, err := ExecuteInvocation(context.Background(), n, selection)
	if err != nil {
		t.Fatal(err)
	}
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("witness behind a pipe hold = %v, want HEALTHY", got)
	}
	if len(tests) == 0 {
		t.Fatal("the witness stream behind a pipe hold produced no outcomes")
	}
}

// The normalization's snapshot is judged whole: a toolchain answering
// no GOVERSION, GOOS, or GOARCH (a wrapper filtering the document) is
// refused, never pinned as empty values (REQ-policy-explicit).
func TestNormalizationRefusesAPartialEnvironmentDocument(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = env ] && [ \"$2\" = -json ]; then echo '{\"GOOS\":\"linux\"}'; exit 0; fi\nexec \"" + goBinary + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	neutralAmbient(t)
	dir := discoverFixture(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	if _, err := NormalizeInvocation(context.Background(), dir, goInvocation("partial", cfg)); err == nil || !strings.Contains(err.Error(), "answered no GOVERSION") {
		t.Fatalf("a partial environment document = %v, want the missing key named", err)
	}
}
