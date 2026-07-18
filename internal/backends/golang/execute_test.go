package golang

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// executeFixture is the workspace fixture the executor tests share: a root
// module carrying one package per failure class and a workspace member
// whose suite fails.
func executeFixture(t *testing.T) string {
	t.Helper()
	dir := discoverFixture(t)
	return strings.TrimSuffix(dir, "discover") + "execute"
}

// executeInvocation normalizes and discovers one invocation over the
// execute fixture, then runs it.
func executeInvocation(t *testing.T, timeout time.Duration, cfg *stipulatorv1.GoInvocationConfig, name string) (*stipulatorv1.InvocationHealth, []*stipulatorv1.TestResult, []*stipulatorv1.FailureDiagnostic) {
	t.Helper()
	health, tests, diags, _ := executeInvocationObserved(t, timeout, cfg, name)
	return health, tests, diags
}

// executeInvocationObserved is executeInvocation with the per-process
// observations exposed.
func executeInvocationObserved(t *testing.T, timeout time.Duration, cfg *stipulatorv1.GoInvocationConfig, name string) (*stipulatorv1.InvocationHealth, []*stipulatorv1.TestResult, []*stipulatorv1.FailureDiagnostic, []*ProcessObservation) {
	t.Helper()
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName(name)
	inv.SetTimeout(durationpb.New(timeout))
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, executeFixture(t), inv)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DiscoverInvocation(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	health, tests, diags, observations, err := ExecuteInvocation(ctx, n, obs)
	if err != nil {
		t.Fatal(err)
	}
	return health, tests, diags, observations
}

func packageDisposition(t *testing.T, h *stipulatorv1.InvocationHealth, pkg string) stipulatorv1.HealthDisposition {
	t.Helper()
	for _, p := range h.GetPackages() {
		if p.GetPackage() == pkg {
			return p.GetDisposition()
		}
	}
	t.Fatalf("package %s has no disposition in %v", pkg, h)
	return stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_UNSPECIFIED
}

func findTest(tests []*stipulatorv1.TestResult, pkg, name string) *stipulatorv1.TestResult {
	for _, tr := range tests {
		if tr.GetPackage() == pkg && tr.GetTest() == name {
			return tr
		}
	}
	return nil
}

func findDiagnostic(diags []*stipulatorv1.FailureDiagnostic, pkg, test string) *stipulatorv1.FailureDiagnostic {
	for _, d := range diags {
		if d.GetPackage() == pkg && d.GetTest() == test {
			return d
		}
	}
	return nil
}

// TestGoExecuteHealthyPackagesAndAttribution pins the healthy path: a
// passing package, a build-only package with no test files, and a package
// whose test binary runs no tests all dispose healthy, with every named
// outcome — subtests and skips included — attributed to the producing
// invocation and process.
func TestGoExecuteHealthyPackagesAndAttribution(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete", "REQ-policy-attribution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./ok", "./buildonly", "./notest"})
	health, tests, diags := executeInvocation(t, time.Minute, cfg, "healthy")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("invocation disposition = %v, want HEALTHY (diags: %v)", got, diags)
	}
	for _, pkg := range []string{"example.com/exec/ok", "example.com/exec/buildonly", "example.com/exec/notest"} {
		if got := packageDisposition(t, health, pkg); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
			t.Errorf("package %s = %v, want HEALTHY", pkg, got)
		}
	}
	for name, want := range map[string]stipulatorv1.TestOutcome{
		"TestDouble":      stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED,
		"TestDouble/zero": stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED,
		"TestSkipped":     stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED,
	} {
		tr := findTest(tests, "example.com/exec/ok", name)
		if tr == nil {
			t.Fatalf("no outcome for %s", name)
		}
		if tr.GetOutcome() != want {
			t.Errorf("%s outcome = %v, want %v", name, tr.GetOutcome(), want)
		}
		p := tr.GetProducer()
		if p.GetInvocation() != "healthy" || p.GetProcessId() <= 0 || p.GetProcessOrdinal() < 1 {
			t.Errorf("%s producer = %v, want the producing invocation and process pinned", name, p)
		}
	}
	if len(diags) != 0 {
		t.Errorf("healthy invocation retained diagnostics: %v", diags)
	}
}

// TestGoExecuteBuildFailure pins the build-failure class: a package that
// does not compile disposes BUILD_FAILED — distinct from a test failure —
// with the compiler output retained.
func TestGoExecuteBuildFailure(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./builderr"})
	health, _, diags := executeInvocation(t, time.Minute, cfg, "builderr")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED {
		t.Fatalf("invocation disposition = %v, want BUILD_FAILED", got)
	}
	d := findDiagnostic(diags, "example.com/exec/builderr", "")
	if d == nil {
		t.Fatal("no package diagnostic for the build failure")
	}
	if d.GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED {
		t.Errorf("diagnostic disposition = %v, want BUILD_FAILED", d.GetDisposition())
	}
	if !strings.Contains(d.GetOutput(), "undefinedIdentifier") {
		t.Errorf("diagnostic lost the compiler output: %q", d.GetOutput())
	}
}

// TestGoExecuteRedTestMain pins exit-behavior conservation: a TestMain
// that exits non-zero after a green run fails the package exactly as a
// direct `go test` would, while the green outcomes it produced remain
// recorded.
func TestGoExecuteRedTestMain(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./redmain"})
	health, tests, diags := executeInvocation(t, time.Minute, cfg, "redmain")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("invocation disposition = %v, want TEST_FAILED", got)
	}
	tr := findTest(tests, "example.com/exec/redmain", "TestGreen")
	if tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
		t.Errorf("TestGreen outcome = %v, want the passing outcome recorded beside the red package", tr)
	}
	if d := findDiagnostic(diags, "example.com/exec/redmain", ""); d == nil {
		t.Error("no package diagnostic for the red TestMain exit")
	}
}

// TestGoExecuteDependencyBuildFailure pins build-failure conservation
// across package boundaries: a selected package whose dependency fails to
// compile disposes BUILD_FAILED itself, with the culprit dependency named
// in the retained compiler output.
func TestGoExecuteDependencyBuildFailure(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./depbad"})
	health, _, diags := executeInvocation(t, time.Minute, cfg, "depbad")
	if got := packageDisposition(t, health, "example.com/exec/depbad"); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED {
		t.Fatalf("selected package disposition = %v, want BUILD_FAILED for a dependency build failure", got)
	}
	d := findDiagnostic(diags, "example.com/exec/depbad", "")
	if d == nil || !strings.Contains(d.GetOutput(), "example.com/exec/builderr") {
		t.Errorf("culprit dependency not named in the diagnostic: %v", d)
	}
}

// TestGoExecuteInitFailure pins init conservation: a package whose init
// panics fails before any test runs, disposing TEST_FAILED with the init
// panic retained in the package diagnostic and no test outcome invented.
func TestGoExecuteInitFailure(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./initred"})
	health, tests, diags := executeInvocation(t, time.Minute, cfg, "initred")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("invocation disposition = %v, want TEST_FAILED", got)
	}
	if tr := findTest(tests, "example.com/exec/initred", "TestNeverRuns"); tr != nil {
		t.Errorf("a test that never ran gained an outcome: %v", tr)
	}
	d := findDiagnostic(diags, "example.com/exec/initred", "")
	if d == nil || !strings.Contains(d.GetOutput(), "panic: init red") {
		t.Errorf("init panic not retained in the package diagnostic: %v", d)
	}
}

// TestGoExecutePackagePanic pins the panic class: a panicking test fails
// its package with the panic retained in the test's diagnostic.
func TestGoExecutePackagePanic(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./panics"})
	health, tests, diags := executeInvocation(t, time.Minute, cfg, "panics")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("invocation disposition = %v, want TEST_FAILED", got)
	}
	tr := findTest(tests, "example.com/exec/panics", "TestPanics")
	if tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
		t.Fatalf("TestPanics outcome = %v, want FAILED", tr)
	}
	d := findDiagnostic(diags, "example.com/exec/panics", "TestPanics")
	if d == nil || !strings.Contains(d.GetOutput(), "fixture panic") {
		t.Errorf("panic output not retained: %v", d)
	}
}

// TestGoExecuteEnvelopeTimeout pins the invocation envelope: when the
// reviewed timeout expires, the invocation and its unfinished packages
// dispose TIMEOUT — a terminal reported fact, not an error and not a
// discarded run.
func TestGoExecuteEnvelopeTimeout(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit", "REQ-go-policy-complete")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./sleepy"})
	// Bypass the toolchain's result cache so the envelope demonstrably
	// expires over a real run rather than a served cache hit.
	cfg.SetCacheMode(stipulatorv1.GoCacheMode_GO_CACHE_MODE_BYPASS)
	health, _, diags := executeInvocation(t, time.Second, cfg, "sleepy-envelope")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Fatalf("invocation disposition = %v, want TIMEOUT", got)
	}
	if got := packageDisposition(t, health, "example.com/exec/sleepy"); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Errorf("package disposition = %v, want TIMEOUT", got)
	}
	if d := findDiagnostic(diags, "example.com/exec/sleepy", ""); d == nil {
		t.Error("no diagnostic for the envelope timeout")
	}
}

// TestGoExecuteGoTestLevelTimeout pins the go-test-level timeout class: a
// test binary aborted by its own -test.timeout fails the package exactly
// as a direct `go test` would, with the timeout panic retained. The
// timeout rides the typed args field — arguments handed to the test
// binary — never an invented flag.
func TestGoExecuteGoTestLevelTimeout(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./sleepy"})
	cfg.SetArgs([]string{"-test.timeout=250ms"})
	health, _, diags := executeInvocation(t, time.Minute, cfg, "sleepy-toolchain")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("invocation disposition = %v, want TEST_FAILED", got)
	}
	d := findDiagnostic(diags, "example.com/exec/sleepy", "")
	if d == nil || !strings.Contains(d.GetOutput(), "test timed out") {
		t.Errorf("timeout panic not retained in the package diagnostic: %v", d)
	}
}

// TestGoExecuteExamples pins executable-example conservation: a passing
// example passes, a failing example fails its package, and the got/want
// mismatch is retained.
func TestGoExecuteExamples(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./examples"})
	health, tests, diags := executeInvocation(t, time.Minute, cfg, "examples")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("invocation disposition = %v, want TEST_FAILED", got)
	}
	if tr := findTest(tests, "example.com/exec/examples", "Example_pass"); tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
		t.Errorf("Example_pass outcome = %v, want PASSED", tr)
	}
	if tr := findTest(tests, "example.com/exec/examples", "Example_fail"); tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
		t.Errorf("Example_fail outcome = %v, want FAILED", tr)
	}
	d := findDiagnostic(diags, "example.com/exec/examples", "Example_fail")
	if d == nil || !strings.Contains(d.GetOutput(), "actual output") {
		t.Errorf("example mismatch output not retained: %v", d)
	}
}

// TestGoExecuteFuzzSeedReplayFailure pins committed-seed conservation: a
// failing committed seed fails its fuzz target's deterministic replay,
// named per seed.
func TestGoExecuteFuzzSeedReplayFailure(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./fuzzseed"})
	health, tests, _ := executeInvocation(t, time.Minute, cfg, "fuzzseed")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("invocation disposition = %v, want TEST_FAILED", got)
	}
	if tr := findTest(tests, "example.com/exec/fuzzseed", "FuzzRefuse/seed-red"); tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
		t.Errorf("seed replay outcome = %v, want the named seed FAILED", tr)
	}
}

// TestGoExecutePolicyWorkspaceReport pins one policy execution end to
// end: every workspace member executes, every selected package and
// invocation carries a terminal disposition, the conservation findings
// are empty for a complete policy, and a failing member fails its own
// invocation.
func TestGoExecutePolicyWorkspaceReport(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete", "REQ-policy-conservation", "REQ-policy-attribution")
	neutralAmbient(t)
	p := &stipulatorv1.TestPolicy{}
	memberCfg := &stipulatorv1.GoInvocationConfig{}
	memberCfg.SetModuleRoot("member")
	memberCfg.SetPackages([]string{"./..."})
	rootCfg := &stipulatorv1.GoInvocationConfig{}
	rootCfg.SetPackages([]string{"./..."})
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("member", memberCfg),
		goInvocation("root", rootCfg),
	})
	report, _, err := ExecutePolicy(context.Background(), executeFixture(t), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.GetInvocations()) != 2 {
		t.Fatalf("report carries %d invocations, want 2", len(report.GetInvocations()))
	}
	byName := map[string]*stipulatorv1.InvocationHealth{}
	for _, h := range report.GetInvocations() {
		byName[h.GetInvocation()] = h
	}
	if got := byName["member"].GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Errorf("member invocation = %v, want TEST_FAILED", got)
	}
	// The root invocation aggregates its worst package: the build failure.
	if got := byName["root"].GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED {
		t.Errorf("root invocation = %v, want BUILD_FAILED", got)
	}
	want := map[string]stipulatorv1.HealthDisposition{
		"example.com/exec/ok":        stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
		"example.com/exec/buildonly": stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
		"example.com/exec/notest":    stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
		"example.com/exec/builderr":  stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED,
		"example.com/exec/depbad":    stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED,
		"example.com/exec/initred":   stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
		"example.com/exec/redmain":   stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
		"example.com/exec/panics":    stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
		"example.com/exec/sleepy":    stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
		"example.com/exec/examples":  stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
		"example.com/exec/fuzzseed":  stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
		"example.com/exec/reads":     stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
		"example.com/exec/killmid":   stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
		"example.com/exec/mainexit":  stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
		"example.com/exec/mixed":     stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
	}
	for pkg, wantD := range want {
		if got := packageDisposition(t, byName["root"], pkg); got != wantD {
			t.Errorf("root package %s = %v, want %v", pkg, got, wantD)
		}
	}
	if got := packageDisposition(t, byName["member"], "example.com/execmember"); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Errorf("member package = %v, want TEST_FAILED", got)
	}
	if tr := findTest(report.GetTests(), "example.com/execmember", "TestAnswer"); tr == nil ||
		tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED || tr.GetProducer().GetInvocation() != "member" {
		t.Errorf("member failure not attributed: %v", tr)
	}
	// A policy selecting every universe obligation exactly once yields no
	// conservation findings.
	if len(report.GetObligations()) != 0 {
		t.Errorf("complete policy reported findings: %v", report.GetObligations())
	}
	// Every launched process owns exactly one observation on the report,
	// bound to its producer — one per selected package here, since every
	// package spawns a process.
	launched := len(byName["root"].GetPackages()) + len(byName["member"].GetPackages())
	if got := len(report.GetObservations()); got != launched {
		t.Errorf("report carries %d observations, want one per launched process (%d)", got, launched)
	}
	for _, o := range report.GetObservations() {
		if o.GetProducer().GetInvocation() == "" || o.GetProducer().GetProcessId() <= 0 {
			t.Errorf("observation not bound to a producing process: %v", o)
		}
		if (o.GetCompleted() == nil) == (o.GetIncompleteReason() == "") {
			t.Errorf("observation is neither completed nor loudly incomplete: %v", o)
		}
	}
}

// TestGoExecutePolicyReportsOmissions pins the conservation half of one
// execution: a policy omitting a member's obligations reports every
// omission beside the executed invocations, never silence.
func TestGoExecutePolicyReportsOmissions(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-conservation")
	neutralAmbient(t)
	p := &stipulatorv1.TestPolicy{}
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetModuleRoot("member")
	cfg.SetPackages([]string{"./..."})
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("member", cfg)})
	report, _, err := ExecutePolicy(context.Background(), executeFixture(t), p)
	if err != nil {
		t.Fatal(err)
	}
	omitted := map[string]bool{}
	for _, o := range report.GetObligations() {
		if o.GetDisposition() == stipulatorv1.ObligationDisposition_OBLIGATION_DISPOSITION_OMITTED {
			omitted[o.GetObligation()] = true
		}
	}
	for _, id := range []string{
		"package:example.com/exec/ok",
		"test:example.com/exec/ok.TestDouble",
		"example:example.com/exec/examples.Example_fail",
		"seed:example.com/exec/fuzzseed.FuzzRefuse/seed-red",
	} {
		if !omitted[id] {
			t.Errorf("omitted obligation %s not reported", id)
		}
	}
}

// TestGoExecuteCancellationDiscardsPartialReport pins the discard
// contract: a cancelled execution yields no invocation health, no test
// outcome, no diagnostic — only the cancellation error.
func TestGoExecuteCancellationDiscardsPartialReport(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-cancellation")
	neutralAmbient(t)
	fixture := executeFixture(t)
	inv := goInvocation("sleepy", func() *stipulatorv1.GoInvocationConfig {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./sleepy", "./ok"})
		// Bypass the toolchain's result cache so the run demonstrably
		// outlives the cancellation instead of completing from cache
		// before it fires.
		cfg.SetCacheMode(stipulatorv1.GoCacheMode_GO_CACHE_MODE_BYPASS)
		return cfg
	}())
	ctx, cancel := context.WithCancel(context.Background())
	n, err := NormalizeInvocation(ctx, fixture, inv)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DiscoverInvocation(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	health, tests, diags, observations, err := ExecuteInvocation(ctx, n, obs)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if health != nil || tests != nil || diags != nil || observations != nil {
		t.Fatalf("partial results escaped a cancelled execution: %v %v %v %v", health, tests, diags, observations)
	}

	// The policy path discards identically.
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{inv})
	pctx, pcancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		pcancel()
	}()
	report, live, err := ExecutePolicy(pctx, fixture, p)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("policy err = %v, want context.Canceled", err)
	}
	if report != nil || live != nil {
		t.Fatalf("partial report escaped a cancelled policy execution: %v %v", report, live)
	}
}

// TestGoExecuteRefusesSilentStream pins the refusal ladder for silence: a
// command that produces no events is DEGRADED — named distinctly from a
// test failure — whether it exits zero or not.
func TestGoExecuteRefusesSilentStream(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	for _, tc := range []struct {
		name    string
		waitErr error
	}{
		{"exit zero with no output", nil},
		{"exit failure with no output", errors.New("exit status 1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := parseTestStream("inv", "example.com/x", strings.NewReader(""), nil)
			run := classifyRun("inv", "example.com/x", st, tc.waitErr, &boundedBuffer{})
			if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
				t.Fatalf("disposition = %v, want DEGRADED, never healthy and never a test failure", run.disposition)
			}
			if len(run.diags) != 1 || !strings.Contains(run.diags[0].GetOutput(), "silent command stream") {
				t.Errorf("silence not named in the diagnostic: %v", run.diags)
			}
		})
	}
}

// TestGoExecuteRefusesMalformedStream pins the refusal ladder for
// malformed output: an unparseable line anywhere in the event stream —
// before or after the terminal package event — degrades the package,
// retaining the offending bytes. Malformation beats a terminal verdict:
// a poisoned stream is never trusted, even about its own success.
func TestGoExecuteRefusesMalformedStream(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	for name, stream := range map[string]string{
		"garbage before the terminal event": `{"Action":"start","Package":"example.com/x"}` + "\n" +
			"garbage interleaved line\n" +
			`{"Action":"pass","Package":"example.com/x"}` + "\n",
		"garbage after the terminal event": `{"Action":"start","Package":"example.com/x"}` + "\n" +
			`{"Action":"pass","Package":"example.com/x"}` + "\n" +
			"garbage trailing line\n",
	} {
		t.Run(name, func(t *testing.T) {
			st := parseTestStream("inv", "example.com/x", strings.NewReader(stream), nil)
			run := classifyRun("inv", "example.com/x", st, nil, &boundedBuffer{})
			if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
				t.Fatalf("disposition = %v, want DEGRADED: a poisoned stream is never trusted", run.disposition)
			}
			if len(run.diags) != 1 || !strings.Contains(run.diags[0].GetOutput(), "unparseable") {
				t.Errorf("malformed bytes not named in the diagnostic: %v", run.diags)
			}
		})
	}
}

// TestGoExecuteSpawnRefusedByExpiredContext pins the spawn-path guard: a
// package whose process spawn is refused by an already expired or
// cancelled context reports no terminal fact of its own — the caller
// classifies it as timeout or discards it — never an environmental
// degradation.
func TestGoExecuteSpawnRefusedByExpiredContext(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n := &NormalizedInvocation{Name: "expired", Dir: t.TempDir(), Timeout: time.Minute}
	run := runPackage(ctx, n, "example.com/x", nil, 1)
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_UNSPECIFIED {
		t.Fatalf("disposition = %v, want none: the caller owns the timeout-or-discard classification", run.disposition)
	}
	if len(run.diags) != 0 {
		t.Errorf("a refused spawn fabricated diagnostics: %v", run.diags)
	}
}

// TestGoExecuteCommandArgsRendering pins the typed-configuration flag
// rendering: race, tags, module mode, PGO (keyword and tree-relative
// path), count, cache bypass, test-binary args, and the executor's
// top-level test selection each render exactly their reviewed form,
// nothing ambient. Selection renders as an anchored, regexp-escaped
// `-run` go-command flag before the package argument — never a binary
// argument.
func TestGoExecuteCommandArgsRendering(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete", "REQ-policy-explicit", "REQ-core-one-execution")
	sep := string(filepath.Separator)
	tree := sep + filepath.Join("host", "tree")
	for name, tc := range map[string]struct {
		n         *NormalizedInvocation
		selection []string
		want      []string
	}{
		"bare": {
			n:    &NormalizedInvocation{Dir: tree},
			want: []string{"test", "-json", "-timeout=0", "pkg"},
		},
		"race": {
			n:    &NormalizedInvocation{Dir: tree, Race: true},
			want: []string{"test", "-json", "-timeout=0", "-race", "pkg"},
		},
		"tags": {
			n:    &NormalizedInvocation{Dir: tree, Tags: []string{"a", "b"}},
			want: []string{"test", "-json", "-timeout=0", "-tags=a,b", "pkg"},
		},
		"module mode": {
			n:    &NormalizedInvocation{Dir: tree, ModuleMode: stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR},
			want: []string{"test", "-json", "-timeout=0", "-mod=vendor", "pkg"},
		},
		"pgo keyword": {
			n:    &NormalizedInvocation{Dir: tree, PGO: "off"},
			want: []string{"test", "-json", "-timeout=0", "-pgo=off", "pkg"},
		},
		"pgo tree-relative path from a nested module root": {
			n: &NormalizedInvocation{
				Dir:        filepath.Join(tree, "member"),
				ModuleRoot: "member",
				PGO:        "profiles/cpu.pprof",
			},
			want: []string{"test", "-json", "-timeout=0", "-pgo=" + filepath.Join(tree, "profiles", "cpu.pprof"), "pkg"},
		},
		"count": {
			n:    &NormalizedInvocation{Dir: tree, Count: 3},
			want: []string{"test", "-json", "-timeout=0", "-count=3", "pkg"},
		},
		"cache bypass": {
			n:    &NormalizedInvocation{Dir: tree, CacheBypass: true},
			want: []string{"test", "-json", "-timeout=0", "-count=1", "pkg"},
		},
		"test binary args": {
			n:    &NormalizedInvocation{Dir: tree, Args: []string{"-test.timeout=1s", "extra"}},
			want: []string{"test", "-json", "-timeout=0", "pkg", "-args", "-test.timeout=1s", "extra"},
		},
		"selection": {
			n:         &NormalizedInvocation{Dir: tree},
			selection: []string{"TestOne", "FuzzTwo"},
			want:      []string{"test", "-json", "-timeout=0", "-run=^(TestOne|FuzzTwo)$", "pkg"},
		},
		"selection escapes regexp metacharacters": {
			n:         &NormalizedInvocation{Dir: tree},
			selection: []string{"TestDot.Star*"},
			want:      []string{"test", "-json", "-timeout=0", `-run=^(TestDot\.Star\*)$`, "pkg"},
		},
		"selection rides the go command, never the binary args": {
			n:         &NormalizedInvocation{Dir: tree, Args: []string{"-test.timeout=1s"}},
			selection: []string{"TestOne"},
			want:      []string{"test", "-json", "-timeout=0", "-run=^(TestOne)$", "pkg", "-args", "-test.timeout=1s"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := testCommandArgs(tc.n, "pkg", tc.selection, "")
			if !slices.Equal(got, tc.want) {
				t.Errorf("testCommandArgs = %q, want %q", got, tc.want)
			}
		})
	}
	// The per-process testlog capture file rides first among the binary
	// arguments; validation refuses any reviewed args entry naming the
	// flag, so the capture is always the executor's own file.
	t.Run("testlog capture", func(t *testing.T) {
		n := &NormalizedInvocation{Dir: tree, Args: []string{"extra"}}
		want := []string{"test", "-json", "-timeout=0", "pkg", "-args", "-test.testlogfile=/tmp/log", "extra"}
		if got := testCommandArgs(n, "pkg", nil, "/tmp/log"); !slices.Equal(got, want) {
			t.Errorf("testCommandArgs = %q, want %q", got, want)
		}
	})
}

// executeSelection normalizes one invocation over the execute fixture and
// runs a witness-only selection of it.
func executeSelection(t *testing.T, timeout time.Duration, cfg *stipulatorv1.GoInvocationConfig, name string, sel TestSelection) *SelectionResult {
	t.Helper()
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName(name)
	inv.SetTimeout(durationpb.New(timeout))
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, executeFixture(t), inv)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ExecuteSelection(ctx, n, sel)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func findProcess(procs []ProcessOutcome, pkg, test string) *ProcessOutcome {
	for i := range procs {
		if procs[i].Package == pkg && procs[i].Test == test {
			return &procs[i]
		}
	}
	return nil
}

// sameProcess reports whether two producer identities name the same
// launched process of one execution.
func sameProcess(a, b *stipulatorv1.ProducerIdentity) bool {
	return a != nil && b != nil &&
		a.GetInvocation() == b.GetInvocation() &&
		a.GetProcessOrdinal() == b.GetProcessOrdinal()
}

// TestGoExecuteSelectionRunsOnlySelected pins the selective execution's
// narrowing: a top-level selection executes exactly the named runnables —
// subtests riding their selected parent — and the unselected sibling
// produces no outcome at all, from one healthy package-selection process
// whose producer is pinned.
func TestGoExecuteSelectionRunsOnlySelected(t *testing.T) {
	stipulate.Covers(t, "REQ-core-one-execution", "REQ-policy-attribution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./ok"})
	res := executeSelection(t, time.Minute, cfg, "selective", TestSelection{
		"example.com/exec/ok": {"TestDouble"},
	})
	for name, want := range map[string]stipulatorv1.TestOutcome{
		"TestDouble":      stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED,
		"TestDouble/zero": stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED,
	} {
		tr := findTest(res.Tests, "example.com/exec/ok", name)
		if tr == nil || tr.GetOutcome() != want {
			t.Errorf("%s outcome = %v, want %v", name, tr, want)
		}
	}
	if tr := findTest(res.Tests, "example.com/exec/ok", "TestSkipped"); tr != nil {
		t.Errorf("unselected test executed: %v", tr)
	}
	if len(res.Processes) != 1 {
		t.Fatalf("selective run launched %d processes, want 1: %v", len(res.Processes), res.Processes)
	}
	p := res.Processes[0]
	if p.Test != "" || p.Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Errorf("package-selection process = %+v, want a healthy package process", p)
	}
	if p.Producer.GetInvocation() != "selective" || p.Producer.GetProcessId() <= 0 {
		t.Errorf("process producer = %v, want the producing invocation and process pinned", p.Producer)
	}
}

// TestGoExecuteSelectionIsolatesAbortShadowedTests pins the isolation
// pass's abort class: a process a sibling kills mid-run denies its
// shadowed tests any outcome, so each denied test is re-run solo and
// gains its outcome from its own producing process — while the killer's
// re-run dies again and its failure stands, no outcome invented.
func TestGoExecuteSelectionIsolatesAbortShadowedTests(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-core-one-execution", "REQ-policy-attribution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./killmid"})
	res := executeSelection(t, time.Minute, cfg, "isolate-abort", TestSelection{
		"example.com/exec/killmid": {"TestKilledMidRun", "TestShadowedByKill"},
	})
	main := findProcess(res.Processes, "example.com/exec/killmid", "")
	if main == nil || main.Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("package-selection process = %+v, want TEST_FAILED for the killed package", main)
	}
	shadow := findProcess(res.Processes, "example.com/exec/killmid", "TestShadowedByKill")
	if shadow == nil || shadow.Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("shadowed test's solo process = %+v, want a healthy solo re-run", shadow)
	}
	if sameProcess(shadow.Producer, main.Producer) {
		t.Error("solo re-run attributed to the killed process, want its own producer")
	}
	tr := findTest(res.Tests, "example.com/exec/killmid", "TestShadowedByKill")
	if tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
		t.Fatalf("shadowed test outcome = %v, want PASSED from the solo re-run", tr)
	}
	if !sameProcess(tr.GetProducer(), shadow.Producer) {
		t.Errorf("shadowed test producer = %v, want the solo process %v", tr.GetProducer(), shadow.Producer)
	}
	// The killer re-runs solo once, dies again, and its failure stands:
	// no outcome is invented for it.
	killer := findProcess(res.Processes, "example.com/exec/killmid", "TestKilledMidRun")
	if killer == nil || killer.Disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Errorf("killer's solo process = %+v, want its own death recorded", killer)
	}
	if tr := findTest(res.Tests, "example.com/exec/killmid", "TestKilledMidRun"); tr != nil {
		t.Errorf("a test that never finished gained an outcome: %v", tr)
	}
}

// TestGoExecuteSelectionIsolatesGreenInRedProcess pins the isolation
// pass's red-process class: a completed pass inside a process whose own
// disposition is red grants no green evidence from that process, so the
// pass is re-run solo and gains its outcome from a healthy process of its
// own — with a completed observation — while the red sibling's failure
// stands and is never re-run.
func TestGoExecuteSelectionIsolatesGreenInRedProcess(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-policy-attribution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./mixed"})
	res := executeSelection(t, time.Minute, cfg, "isolate-green", TestSelection{
		"example.com/exec/mixed": {"TestGreen", "TestRed"},
	})
	main := findProcess(res.Processes, "example.com/exec/mixed", "")
	if main == nil || main.Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("package-selection process = %+v, want TEST_FAILED to stand", main)
	}
	solo := findProcess(res.Processes, "example.com/exec/mixed", "TestGreen")
	if solo == nil || solo.Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("green test's solo process = %+v, want a healthy solo re-run", solo)
	}
	if p := findProcess(res.Processes, "example.com/exec/mixed", "TestRed"); p != nil {
		t.Errorf("failed test re-run in isolation, want its failure to stand: %+v", p)
	}
	if tr := findTest(res.Tests, "example.com/exec/mixed", "TestRed"); tr == nil ||
		tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
		t.Errorf("red sibling outcome = %v, want FAILED recorded", tr)
	}
	var fromSolo bool
	for _, tr := range res.Tests {
		if tr.GetTest() == "TestGreen" && sameProcess(tr.GetProducer(), solo.Producer) {
			if tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
				t.Errorf("solo re-run outcome = %v, want PASSED", tr.GetOutcome())
			}
			fromSolo = true
		}
	}
	if !fromSolo {
		t.Error("green test gained no outcome attributed to its solo process")
	}
	var soloObserved bool
	for _, o := range res.Observations {
		if sameProcess(o.Wire.GetProducer(), solo.Producer) {
			if o.Wire.GetCompleted() == nil {
				t.Errorf("solo healthy process observation = %v, want completed", o.Wire)
			}
			soloObserved = true
		}
	}
	if !soloObserved {
		t.Error("solo healthy process owns no observation")
	}
}

// TestGoExecuteSelectionFuzzReplaysCommittedSeeds pins the fuzz leg of
// selective execution: a single-element Fuzz selection replays the
// target's committed seeds — each seed row appears with its replay
// outcome, here the red seed failing — the target's own failure stands
// (never isolation-eligible), and nothing outside the selected target
// executes.
func TestGoExecuteSelectionFuzzReplaysCommittedSeeds(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-go-policy-complete")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./fuzzseed"})
	res := executeSelection(t, time.Minute, cfg, "fuzz-replay", TestSelection{
		"example.com/exec/fuzzseed": {"FuzzRefuse"},
	})
	if tr := findTest(res.Tests, "example.com/exec/fuzzseed", "FuzzRefuse/seed-red"); tr == nil ||
		tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
		t.Fatalf("committed seed replay outcome = %v, want the named seed FAILED", tr)
	}
	for _, tr := range res.Tests {
		if topLevel(tr.GetTest()) != "FuzzRefuse" {
			t.Errorf("unselected runnable executed: %v", tr)
		}
	}
	main := findProcess(res.Processes, "example.com/exec/fuzzseed", "")
	if main == nil || main.Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("package-selection process = %+v, want TEST_FAILED from the red seed", main)
	}
	// The target's recorded failure stands: the isolation pass never
	// re-runs it, so the package process is the execution's only one.
	if len(res.Processes) != 1 {
		t.Errorf("failing seed replay spawned isolation processes: %+v", res.Processes)
	}
}

// TestGoExecuteRefusesTruncatedStream pins the refusal ladder for missing
// terminals: a stream that ends without a terminal package event —
// a killed binary, a truncated pipe — is DEGRADED.
func TestGoExecuteRefusesTruncatedStream(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	stream := `{"Action":"start","Package":"example.com/x"}` + "\n" +
		`{"Action":"run","Package":"example.com/x","Test":"TestX"}` + "\n"
	st := parseTestStream("inv", "example.com/x", strings.NewReader(stream), nil)
	run := classifyRun("inv", "example.com/x", st, nil, &boundedBuffer{})
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
		t.Fatalf("disposition = %v, want DEGRADED for a stream without a terminal event", run.disposition)
	}
}

// TestGoExecuteRefusesGreenStreamRedExit pins the exit cross-check: a
// passing stream from a process that exited non-zero is a contradiction,
// disposed DEGRADED rather than trusted in either direction.
func TestGoExecuteRefusesGreenStreamRedExit(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	stream := `{"Action":"start","Package":"example.com/x"}` + "\n" +
		`{"Action":"pass","Package":"example.com/x"}` + "\n"
	st := parseTestStream("inv", "example.com/x", strings.NewReader(stream), nil)
	run := classifyRun("inv", "example.com/x", st, errors.New("exit status 2"), &boundedBuffer{})
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
		t.Fatalf("disposition = %v, want DEGRADED for a green stream from a red process", run.disposition)
	}
}

// TestGoExecuteDiagnosticOutputBounded pins the retention cap: a failing
// test with pathological output yields a diagnostic capped at the
// executor's bound with truncation marked, never an unbounded report and
// never silent truncation.
func TestGoExecuteDiagnosticOutputBounded(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	var b strings.Builder
	b.WriteString(`{"Action":"run","Package":"example.com/x","Test":"TestBig"}` + "\n")
	line := strings.Repeat("x", 1024)
	for range 2 * failureOutputCap / len(line) {
		b.WriteString(`{"Action":"output","Package":"example.com/x","Test":"TestBig","Output":"` + line + `"}` + "\n")
	}
	b.WriteString(`{"Action":"fail","Package":"example.com/x","Test":"TestBig"}` + "\n")
	b.WriteString(`{"Action":"fail","Package":"example.com/x"}` + "\n")
	st := parseTestStream("inv", "example.com/x", strings.NewReader(b.String()), nil)
	run := classifyRun("inv", "example.com/x", st, errors.New("exit status 1"), &boundedBuffer{})
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("disposition = %v, want TEST_FAILED", run.disposition)
	}
	d := findDiagnostic(run.diags, "example.com/x", "TestBig")
	if d == nil {
		t.Fatal("no diagnostic for the failing test")
	}
	if len(d.GetOutput()) > failureOutputCap {
		t.Errorf("diagnostic output %d bytes exceeds the %d cap", len(d.GetOutput()), failureOutputCap)
	}
	if !d.GetTruncated() {
		t.Error("truncation not marked on a capped diagnostic")
	}
}
