package golang

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	stipulate.Covers(t, "REQ-policy-explicit", "REQ-go-policy-complete", "REQ-policy-budget-attribution")
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
	d := findDiagnostic(diags, "example.com/exec/sleepy", "")
	if d == nil {
		t.Fatal("no diagnostic for the envelope timeout")
	}
	// The bound is always named; the cut-off subject roster depends on
	// whether the envelope outlived the build phase, so its rendering is
	// pinned deterministically by
	// TestGoExecuteEnvelopeTimeoutListsCutOffSubjects.
	if !strings.Contains(d.GetOutput(), "invocation timeout 1s expired before the package completed") {
		t.Errorf("envelope bound not named in the diagnostic: %q", d.GetOutput())
	}
}

// TestGoExecuteEnvelopeTimeoutListsCutOffSubjects pins the envelope
// arm's attribution rendering: the classified TIMEOUT diagnostic names
// the reviewed envelope bound and lists the subjects the cutoff left
// unfinished — denied a completed measurement, never reported failed.
func TestGoExecuteEnvelopeTimeoutListsCutOffSubjects(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-budget-attribution")
	n := &NormalizedInvocation{Name: "inv", Timeout: 90 * time.Second}
	r := packageRun{pkg: "example.com/x", aborted: []string{"TestCut", "TestAlsoCut"}}
	if err := finalizeRun(n, &r, true, ""); err != nil {
		t.Fatal(err)
	}
	if r.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Fatalf("disposition = %v, want TIMEOUT", r.disposition)
	}
	if len(r.diags) != 1 {
		t.Fatalf("diagnostics = %v, want exactly the timeout diagnostic", r.diags)
	}
	out := r.diags[0].GetOutput()
	if !strings.Contains(out, "invocation timeout 1m30s expired before the package completed") {
		t.Errorf("envelope bound not named: %q", out)
	}
	if !strings.Contains(out, "started but unfinished: TestCut, TestAlsoCut") {
		t.Errorf("cut-off subjects not listed: %q", out)
	}
}

// TestGoExecuteGoTestLevelTimeout pins the binary-deadline class: a test
// binary aborted by its own -test.timeout reds the package as TIMEOUT
// with the exhausted budget named in the diagnostic, and the test the
// deadline cut off publishes no completed outcome — the budget is the
// red fact, never the running test's failure. The timeout rides the
// typed args field — arguments handed to the test binary — never an
// invented flag.
func TestGoExecuteGoTestLevelTimeout(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete", "REQ-policy-budget-attribution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./sleepy"})
	cfg.SetArgs([]string{"-test.timeout=250ms"})
	health, tests, diags := executeInvocation(t, time.Minute, cfg, "sleepy-toolchain")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Fatalf("invocation disposition = %v, want TIMEOUT", got)
	}
	if got := packageDisposition(t, health, "example.com/exec/sleepy"); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Errorf("package disposition = %v, want TIMEOUT", got)
	}
	d := findDiagnostic(diags, "example.com/exec/sleepy", "")
	if d == nil || !strings.Contains(d.GetOutput(), "test binary timeout 250ms exhausted") {
		t.Fatalf("budget not named in the package diagnostic: %v", d)
	}
	if !strings.Contains(d.GetOutput(), "running when the budget expired: TestSleeps") {
		t.Errorf("runtime roster not parsed from the real dump: %q", d.GetOutput())
	}
	if !strings.Contains(d.GetOutput(), "test timed out") {
		t.Errorf("timeout panic not retained in the package diagnostic: %v", d)
	}
	for _, tr := range tests {
		if tr.GetOutcome() == stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
			t.Errorf("deadline victim published a completed FAILED outcome: %s", tr.GetTest())
		}
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
		"example.com/exec/extread":   stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
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
			run := classifyRun("inv", "example.com/x", st, tc.waitErr, &boundedBuffer{}, "")
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
			run := classifyRun("inv", "example.com/x", st, nil, &boundedBuffer{}, "")
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

// executeSelection normalizes and discovers one invocation over the
// execute fixture and runs a witness-only selection of it — discovery
// first, as every production selective path runs it, so the executor has
// the package directories its pre-spawn brackets need.
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
	if _, err := DiscoverInvocation(ctx, n); err != nil {
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

// TestGoExecuteSelectionIsolatesBinaryDeadlineVictims pins the isolation
// pass's deadline class: a binary-deadline TIMEOUT process denies its
// tests exactly as a red suite does — the completed pass inside it is
// voided by the red producer and recovers solo from a healthy process
// of its own (the envelope's budget is intact, and each solo re-run
// carries a fresh binary bound), while the starving test's solo re-run
// starves again under its own bound and gains no invented outcome.
func TestGoExecuteSelectionIsolatesBinaryDeadlineVictims(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-budget-attribution", "REQ-evidence-witness-freshness", "REQ-core-one-execution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./deadline"})
	cfg.SetArgs([]string{"-test.timeout=1s"})
	res := executeSelection(t, time.Minute, cfg, "isolate-deadline", TestSelection{
		"example.com/exec/deadline": {"TestQuick", "TestStall"},
	})
	main := findProcess(res.Processes, "example.com/exec/deadline", "")
	if main == nil || main.Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Fatalf("package-selection process = %+v, want TIMEOUT for the deadline-cut package", main)
	}
	d := findDiagnostic(res.Diagnostics, "example.com/exec/deadline", "")
	if d == nil || !strings.Contains(d.GetOutput(), "test binary timeout 1s exhausted") {
		t.Fatalf("budget not named in the package diagnostic: %v", d)
	}
	if !strings.Contains(d.GetOutput(), "running when the budget expired: TestStall") || strings.Contains(d.GetOutput(), "expired: TestQuick") {
		t.Errorf("roster should name the starving test alone: %q", d.GetOutput())
	}
	quick := findProcess(res.Processes, "example.com/exec/deadline", "TestQuick")
	if quick == nil || quick.Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("voided pass's solo process = %+v, want a healthy solo re-run", quick)
	}
	soloPass := false
	for _, tr := range res.Tests {
		if tr.GetTest() == "TestQuick" && tr.GetOutcome() == stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED && sameProcess(tr.GetProducer(), quick.Producer) {
			soloPass = true
		}
	}
	if !soloPass {
		t.Error("no PASSED outcome attributed to the healthy solo process; the voided pass did not recover")
	}
	stall := findProcess(res.Processes, "example.com/exec/deadline", "TestStall")
	if stall == nil || stall.Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Errorf("starving test's solo process = %+v, want its own TIMEOUT recorded", stall)
	}
	if tr := findTest(res.Tests, "example.com/exec/deadline", "TestStall"); tr != nil {
		t.Errorf("a test the deadline cut off twice gained an outcome: %v", tr)
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
	run := classifyRun("inv", "example.com/x", st, nil, &boundedBuffer{}, "")
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
	run := classifyRun("inv", "example.com/x", st, errors.New("exit status 2"), &boundedBuffer{}, "")
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
		t.Fatalf("disposition = %v, want DEGRADED for a green stream from a red process", run.disposition)
	}
}

// TestGoExecuteBinaryTimeoutLateFlushKeepsCompletedFailure pins the
// deadline classification against the toolchain's real event ordering:
// test2json can flush a completed failure's fail event after the panic
// line, so event ordering never identifies victims — the runtime's own
// "running tests:" roster does. The completed failure keeps its FAILED
// outcome and its own diagnostic; the roster victim gains no outcome
// and no per-test diagnostic and stays in the started set the isolation
// pass consumes; the package reds TIMEOUT naming the reviewed bound,
// never the panic's printed value.
func TestGoExecuteBinaryTimeoutLateFlushKeepsCompletedFailure(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-budget-attribution")
	stream := `{"Action":"run","Package":"example.com/x","Test":"TestRed"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Test":"TestRed","Output":"    x_test.go:4: genuine assertion failure\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Test":"TestRed","Output":"--- FAIL: TestRed (0.00s)\n"}` + "\n" +
		`{"Action":"run","Package":"example.com/x","Test":"TestGhost"}` + "\n" +
		`{"Action":"run","Package":"example.com/x","Test":"TestSlow"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Test":"TestSlow","Output":"panic: test timed out after 301ms\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Test":"TestSlow","Output":"\trunning tests:\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Test":"TestSlow","Output":"\t\tTestSlow (300ms)\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Test":"TestSlow","Output":"goroutine 7 [sleeping]:\n"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x","Test":"TestRed"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x"}` + "\n"
	st := parseTestStream("inv", "example.com/x", strings.NewReader(stream), nil)
	run := classifyRun("inv", "example.com/x", st, errors.New("exit status 1"), &boundedBuffer{}, "300ms")
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Fatalf("disposition = %v, want TIMEOUT", run.disposition)
	}
	if tr := findTest(run.tests, "example.com/x", "TestRed"); tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
		t.Errorf("late-flushed completed failure lost its outcome: %v", tr)
	}
	if d := findDiagnostic(run.diags, "example.com/x", "TestRed"); d == nil || !strings.Contains(d.GetOutput(), "genuine assertion failure") {
		t.Errorf("late-flushed completed failure lost its own diagnostic: %v", d)
	}
	if tr := findTest(run.tests, "example.com/x", "TestSlow"); tr != nil {
		t.Errorf("deadline victim published a completed outcome: %v", tr)
	}
	if d := findDiagnostic(run.diags, "example.com/x", "TestSlow"); d != nil {
		t.Errorf("deadline victim minted a per-test failure diagnostic: %v", d)
	}
	d := findDiagnostic(run.diags, "example.com/x", "")
	if d == nil || !strings.Contains(d.GetOutput(), "test binary timeout 300ms exhausted") {
		t.Fatalf("reviewed bound not named in the package diagnostic: %v", d)
	}
	// The runtime's roster is the victim list, not the started set:
	// TestGhost started and reached no terminal event, yet the runtime
	// did not report it running, so the diagnostic must not list it.
	if !strings.Contains(d.GetOutput(), "running when the budget expired: TestSlow\n") {
		t.Errorf("roster victim not listed alone as running at exhaustion: %q", d.GetOutput())
	}
	started := startedTests(st)
	if !slices.Contains(started, "TestSlow") || slices.Contains(started, "TestRed") {
		t.Errorf("started set = %v, want no completed test in the isolation feed", started)
	}
}

// TestGoExecuteBinaryTimeoutRosterSurvivesInterleavedOutput pins the
// roster scanner against the dump's non-atomicity: goroutines still
// running while the panic dump prints can interleave writes — a bare
// newline included — between roster entries, so any non-matching line
// short of the goroutine section header skips rather than closes; the
// header ends the roster and entry-shaped lines after it stay out.
func TestGoExecuteBinaryTimeoutRosterSurvivesInterleavedOutput(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-budget-attribution")
	stream := `{"Action":"run","Package":"example.com/x","Test":"TestA"}` + "\n" +
		`{"Action":"run","Package":"example.com/x","Test":"TestB"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"panic: test timed out after 1s\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"\trunning tests:\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"\t\tTestA (1s)\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"a concurrent goroutine's own print\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"\t\tTestB (1s)\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"goroutine 9 [running]:\n"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"\t\tFakeEntry (1s)\n"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x"}` + "\n"
	st := parseTestStream("inv", "example.com/x", strings.NewReader(stream), nil)
	run := classifyRun("inv", "example.com/x", st, errors.New("exit status 1"), &boundedBuffer{}, "1s")
	d := findDiagnostic(run.diags, "example.com/x", "")
	if d == nil || !strings.Contains(d.GetOutput(), "running when the budget expired: TestA, TestB\n") {
		t.Errorf("interleaved output corrupted the roster: %v", d)
	}
}

// TestGoExecuteBinaryTimeoutRosterlessDumpFallsBackToStarted pins the
// fallback: a deadline dump carrying no "running tests:" roster still
// lists victims — the started-but-unfinished set stands in.
func TestGoExecuteBinaryTimeoutRosterlessDumpFallsBackToStarted(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-budget-attribution")
	stream := `{"Action":"run","Package":"example.com/x","Test":"TestSlow"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Test":"TestSlow","Output":"panic: test timed out after 250ms\n"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x"}` + "\n"
	st := parseTestStream("inv", "example.com/x", strings.NewReader(stream), nil)
	run := classifyRun("inv", "example.com/x", st, errors.New("exit status 1"), &boundedBuffer{}, "250ms")
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Fatalf("disposition = %v, want TIMEOUT", run.disposition)
	}
	d := findDiagnostic(run.diags, "example.com/x", "")
	if d == nil || !strings.Contains(d.GetOutput(), "running when the budget expired: TestSlow") {
		t.Errorf("started fallback not rendered without a roster: %v", d)
	}
}

// TestGoExecuteBinaryTimeoutGreenStreamNotReclassified pins the guard: a
// passing stream whose test printed the deadline panic's literal line
// stays HEALTHY even under a declared binary bound — a green terminal
// short-circuits before any deadline classification.
func TestGoExecuteBinaryTimeoutGreenStreamNotReclassified(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-budget-attribution")
	stream := `{"Action":"run","Package":"example.com/x","Test":"TestPrints"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Test":"TestPrints","Output":"panic: test timed out after 9s\n"}` + "\n" +
		`{"Action":"pass","Package":"example.com/x","Test":"TestPrints"}` + "\n" +
		`{"Action":"pass","Package":"example.com/x"}` + "\n"
	st := parseTestStream("inv", "example.com/x", strings.NewReader(stream), nil)
	run := classifyRun("inv", "example.com/x", st, nil, &boundedBuffer{}, "9s")
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("disposition = %v, want HEALTHY for a green stream", run.disposition)
	}
	if tr := findTest(run.tests, "example.com/x", "TestPrints"); tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
		t.Errorf("passing test lost its outcome to shape recognition: %v", tr)
	}
}

// TestGoExecuteBinaryTimeoutUndeclaredBoundKeepsFailureClass pins the
// reviewed-record gate: with no binary bound declared in the reviewed
// args there is no budget to exhaust, so the panic shape in a test's
// output — necessarily the test's own print — never reclassifies the
// red, never suppresses a completed failure, and no diagnostic invents
// a budget (REQ-policy-explicit: the record's envelope and reviewed
// arguments are the only sources of execution bounds).
func TestGoExecuteBinaryTimeoutUndeclaredBoundKeepsFailureClass(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-budget-attribution")
	stream := `{"Action":"run","Package":"example.com/x","Test":"TestPrints"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Test":"TestPrints","Output":"panic: test timed out after 9s\n"}` + "\n" +
		`{"Action":"pass","Package":"example.com/x","Test":"TestPrints"}` + "\n" +
		`{"Action":"run","Package":"example.com/x","Test":"TestRed"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x","Test":"TestRed"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x"}` + "\n"
	st := parseTestStream("inv", "example.com/x", strings.NewReader(stream), nil)
	run := classifyRun("inv", "example.com/x", st, errors.New("exit status 1"), &boundedBuffer{}, "")
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("disposition = %v, want TEST_FAILED with no declared binary bound", run.disposition)
	}
	if tr := findTest(run.tests, "example.com/x", "TestRed"); tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
		t.Errorf("genuine failure lost its outcome to a printed panic shape: %v", tr)
	}
	for _, d := range run.diags {
		if strings.Contains(d.GetOutput(), "budget") {
			t.Errorf("diagnostic invents a budget no record declares: %q", d.GetOutput())
		}
	}
}

// TestGoExecuteBinaryTimeoutBuildFailureKeepsClass pins precedence: a
// terminal fail naming a failed build stays BUILD_FAILED even when the
// stream carried the deadline panic's shape under a declared bound — a
// compilation red is never laundered into a budget fact.
func TestGoExecuteBinaryTimeoutBuildFailureKeepsClass(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-budget-attribution")
	stream := `{"Action":"output","Package":"example.com/x","Output":"panic: test timed out after 1s\n"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x","FailedBuild":"example.com/x"}` + "\n"
	st := parseTestStream("inv", "example.com/x", strings.NewReader(stream), nil)
	run := classifyRun("inv", "example.com/x", st, errors.New("exit status 1"), &boundedBuffer{}, "1s")
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED {
		t.Fatalf("disposition = %v, want BUILD_FAILED", run.disposition)
	}
	for _, d := range run.diags {
		if strings.Contains(d.GetOutput(), "budget") {
			t.Errorf("build failure diagnostic carries budget attribution: %q", d.GetOutput())
		}
	}
}

// TestGoExecuteDeclaredBinaryBound pins the reviewed-args extraction the
// classifier's budget naming depends on: last -test.timeout wins in
// both the joined and split spellings, and args declaring none yield
// none.
func TestGoExecuteDeclaredBinaryBound(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-budget-attribution")
	cases := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"-test.v"}, ""},
		{[]string{"-test.timeout=30m"}, "30m"},
		{[]string{"--test.timeout=45s"}, "45s"},
		{[]string{"-test.timeout", "20s"}, "20s"},
		{[]string{"-test.timeout=30m", "-test.timeout=1s"}, "1s"},
		{[]string{"test.timeout=9s"}, ""},
		// Zero and negative disable the runtime's alarm — the spelling
		// that declares NO bound — and a non-duration value declares
		// nothing; none may open the reclassification gate.
		{[]string{"-test.timeout=0"}, ""},
		{[]string{"-test.timeout=-1s"}, ""},
		{[]string{"-test.timeout", "-test.v"}, ""},
		{[]string{"-test.timeout=30m", "-test.timeout=0"}, ""},
	}
	for _, c := range cases {
		n := &NormalizedInvocation{Args: c.args}
		if got := declaredBinaryBound(n); got != c.want {
			t.Errorf("declaredBinaryBound(%v) = %q, want %q", c.args, got, c.want)
		}
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
	run := classifyRun("inv", "example.com/x", st, errors.New("exit status 1"), &boundedBuffer{}, "")
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

// The witness fan-out bound is the invocation's reviewed value when
// set, else half the processor count - each unit is itself a parallel
// process tree, so a full fan-out multiplies into host-freezing load
// (REQ-policy-explicit).
func TestWitnessSpawnBound(t *testing.T) {
	if got := witnessSpawnBound(&NormalizedInvocation{WitnessConcurrency: 3}); got != 3 {
		t.Fatalf("explicit bound = %d, want 3", got)
	}
	want := runtime.GOMAXPROCS(0) / 2
	if want < 1 {
		want = 1
	}
	if got := witnessSpawnBound(&NormalizedInvocation{}); got != want {
		t.Fatalf("default bound = %d, want %d", got, want)
	}
}

// Each unit's inner width is the parent's processor budget over the
// unit bound, floored at one, so units x per-unit width stays at most
// the processor count (the evidence spec's concurrency clause).
func TestWitnessChildWidth(t *testing.T) {
	procs := runtime.GOMAXPROCS(0)
	for _, test := range []struct {
		concurrency int32
		want        int
	}{
		{concurrency: 1, want: procs},
		{concurrency: int32(procs), want: 1},
		{concurrency: int32(10 * procs), want: 1},
	} {
		n := &NormalizedInvocation{WitnessConcurrency: test.concurrency}
		if got := witnessChildWidth(n); got != test.want {
			t.Errorf("width at concurrency %d = %d, want %d", test.concurrency, got, test.want)
		}
	}
	units := witnessSpawnBound(&NormalizedInvocation{})
	width := witnessChildWidth(&NormalizedInvocation{})
	if units*width > procs+units {
		t.Fatalf("default units %d x width %d overcommits %d processors", units, width, procs)
	}
	// The width derives from the invocation's FROZEN fan-out bound —
	// the same value the spawn semaphore reads — never a live
	// re-derivation: two frozen bounds must each yield their own width,
	// which one live-derived value cannot satisfy, and the load-safety
	// product holds against the frozen bound.
	for _, bound := range []int{7, 13} {
		frozen := &NormalizedInvocation{SpawnBound: bound}
		want := procs / bound
		if want < 1 {
			want = 1
		}
		if got := witnessChildWidth(frozen); got != want {
			t.Errorf("width at frozen bound %d = %d, want %d (the frozen value, not a re-derivation)", bound, got, want)
		}
		if got := spawnBoundOf(frozen); got != bound {
			t.Errorf("spawnBoundOf(frozen %d) = %d", bound, got)
		}
	}
}

// The cap rides the single spawn-and-ingest environment source as
// GOMAXPROCS and only ever narrows: an already-narrower ambient value
// is kept, a wider or malformed one is replaced, and the framed arm
// carries the PWD pin beside it (the evidence spec's concurrency
// clause).
func TestWitnessWidthEnv(t *testing.T) {
	procs := runtime.GOMAXPROCS(0)
	full := &NormalizedInvocation{WitnessConcurrency: int32(procs)} // width 1
	if v, ok := lookupEnv(witnessWidthEnv(&NormalizedInvocation{WitnessConcurrency: int32(procs), Env: []string{"A=1"}}), "GOMAXPROCS"); !ok || v != "1" {
		t.Fatalf("injected width = %q/%v, want 1", v, ok)
	}
	if procs > 1 {
		wide := &NormalizedInvocation{WitnessConcurrency: 1, Env: []string{"GOMAXPROCS=1"}} // width procs
		if v, _ := lookupEnv(witnessWidthEnv(wide), "GOMAXPROCS"); v != "1" {
			t.Fatalf("narrower ambient replaced with %q, want kept at 1", v)
		}
	}
	full.Env = []string{"GOMAXPROCS=9999"}
	if v, _ := lookupEnv(witnessWidthEnv(full), "GOMAXPROCS"); v != "1" {
		t.Fatalf("wider ambient = %q, want narrowed to 1", v)
	}
	full.Env = []string{"GOMAXPROCS=lots"}
	if v, _ := lookupEnv(witnessWidthEnv(full), "GOMAXPROCS"); v != "1" {
		t.Fatalf("malformed ambient = %q, want narrowed to 1", v)
	}
	framed := witnessProcessEnv(&NormalizedInvocation{WitnessConcurrency: int32(procs), Env: []string{"A=1"}}, observationFrame{})
	if v, ok := lookupEnv(framed, "GOMAXPROCS"); !ok || v != "1" {
		t.Fatalf("frameless spawn env width = %q/%v, want 1", v, ok)
	}
}

// The witness environment is derived exactly once, at normalization:
// every consumer reads NormalizedInvocation.WitnessEnv, so one
// invocation observes one width for its whole lifetime - per-call
// re-derivation would race dynamic GOMAXPROCS movement and could
// split the spawned environment from the recorded one.
func TestWitnessEnvDerivedOnceAtNormalize(t *testing.T) {
	neutralAmbient(t)
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("width")
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./ok"})
	inv.SetGo(cfg)
	n, err := NormalizeInvocation(context.Background(), executeFixture(t), inv)
	if err != nil {
		t.Fatal(err)
	}
	if n.WitnessEnv == nil {
		t.Fatal("normalization left WitnessEnv underived")
	}
	spawn := witnessProcessEnv(n, observationFrame{})
	if &spawn[0] != &n.WitnessEnv[0] {
		t.Fatal("frameless spawn env re-derived instead of reading the normalization-time witness env")
	}
	if got := witnessEnvOf(n); &got[0] != &n.WitnessEnv[0] {
		t.Fatal("witnessEnvOf re-derived for a normalized invocation")
	}
}

// The cap genuinely reaches the witness process: the armed fixture
// probe passes exactly when the child's GOMAXPROCS equals the derived
// width, and the negative arm - a unit bound of one, whose width is
// the full budget - proves the armed probe runs rather than skips, so
// the positive arm's pass really discriminated the delivered
// environment (the evidence spec's concurrency clause).
func TestGoExecuteDeliversInnerParallelismCap(t *testing.T) {
	neutralAmbient(t)
	t.Setenv("STIPULATOR_FIXTURE_REQUIRE_WIDTH", "1")
	procs := runtime.GOMAXPROCS(0)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./widthprobe"})
	cfg.SetWitnessConcurrency(int32(procs)) // width 1
	health, tests, diags := executeInvocation(t, time.Minute, cfg, "widthcap")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("capped probe disposition = %v, want HEALTHY - the width did not reach the child env (diags: %v)", got, diags)
	}
	tr := findTest(tests, "example.com/exec/widthprobe", "TestWidthDelivered")
	if tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
		t.Fatalf("armed probe = %v, want PASSED under the delivered width", tr)
	}
	if procs == 1 {
		t.Skip("single-processor host: the negative arm's width equals the armed value")
	}
	cfg = &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./widthprobe"})
	cfg.SetWitnessConcurrency(1) // width = procs, not the armed 1
	health, tests, _ = executeInvocation(t, time.Minute, cfg, "widthcap-negative")
	if got := packageDisposition(t, health, "example.com/exec/widthprobe"); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("negative arm disposition = %v, want TEST_FAILED - the armed probe skipped or saw the wrong width", got)
	}
	tr = findTest(tests, "example.com/exec/widthprobe", "TestWidthDelivered")
	if tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
		t.Fatalf("negative-arm probe = %v, want FAILED at the full-budget width", tr)
	}
}

// TestGoBoundedBufferCapCutsAtRuneBoundary pins the retention cap's
// rune safety: process output is bytes, and a cap cut through a
// multi-byte rune would hand a proto string field the invalid UTF-8
// its marshal validation refuses — the cut backs off to a boundary.
func TestGoBoundedBufferCapCutsAtRuneBoundary(t *testing.T) {
	var bb boundedBuffer
	bb.write(strings.Repeat("x", failureOutputCap-1))
	bb.write("日")
	if !utf8.ValidString(bb.b.String()) {
		t.Fatal("the cap cut split a rune; the buffer is not valid UTF-8")
	}
	if !bb.truncated {
		t.Error("a capped write did not mark truncation")
	}
}
