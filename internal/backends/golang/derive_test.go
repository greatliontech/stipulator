package golang

import (
	"context"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"pgregory.net/rapid"

	gofresh "github.com/greatliontech/gofresh"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

const (
	healthy    = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY
	testFailed = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED
	passed     = stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED
	failed     = stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED
	skipped    = stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED
)

// synthInvocation builds one invocation health with per-package
// dispositions and the race rigor flag on its resolved configuration.
func synthInvocation(name string, race bool, pkgs map[string]stipulatorv1.HealthDisposition) *stipulatorv1.InvocationHealth {
	h := &stipulatorv1.InvocationHealth{}
	h.SetInvocation(name)
	rc := &stipulatorv1.GoResolvedConfig{}
	rc.SetRace(race)
	h.SetGo(rc)
	disposition := healthy
	var packages []*stipulatorv1.PackageHealth
	for _, pkg := range slices.Sorted(maps.Keys(pkgs)) {
		ph := &stipulatorv1.PackageHealth{}
		ph.SetPackage(pkg)
		ph.SetDisposition(pkgs[pkg])
		packages = append(packages, ph)
		disposition = worseDisposition(disposition, pkgs[pkg])
	}
	h.SetPackages(packages)
	h.SetDisposition(disposition)
	return h
}

// synthRow builds one attributed test result.
func synthRow(inv, pkg, test string, outcome stipulatorv1.TestOutcome, regs ...string) *stipulatorv1.TestResult {
	producer := &stipulatorv1.ProducerIdentity{}
	producer.SetInvocation(inv)
	producer.SetProcessId(4242)
	producer.SetProcessOrdinal(1)
	tr := &stipulatorv1.TestResult{}
	tr.SetPackage(pkg)
	tr.SetTest(test)
	tr.SetOutcome(outcome)
	tr.SetProducer(producer)
	if len(regs) > 0 {
		tr.SetRegistrations(regs)
	}
	return tr
}

func synthReport(invocations []*stipulatorv1.InvocationHealth, rows []*stipulatorv1.TestResult) *stipulatorv1.ExecutionReport {
	r := &stipulatorv1.ExecutionReport{}
	r.SetInvocations(invocations)
	r.SetTests(rows)
	return r
}

// TestDeriveWitnessRequiresHealthyProducingPackage pins the healthy gate:
// a passing test grants a witness outcome only when its producing package
// disposed healthy inside its producing invocation. A green child of a
// red package — a passing test beside a red TestMain, a passing example
// beside a failing one — records no outcome at all, so a bound test in
// that position reads as unwitnessed; the red results themselves surface
// regardless.
func TestDeriveWitnessRequiresHealthyProducingPackage(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness")
	report := synthReport(
		[]*stipulatorv1.InvocationHealth{synthInvocation("race", true, map[string]stipulatorv1.HealthDisposition{
			"example.com/m/ok":      healthy,
			"example.com/m/redmain": testFailed,
		})},
		[]*stipulatorv1.TestResult{
			synthRow("race", "example.com/m/ok", "TestOk", passed),
			synthRow("race", "example.com/m/redmain", "TestGreen", passed),
			synthRow("race", "example.com/m/redmain", "TestGreen/sub", passed),
			synthRow("race", "example.com/m/redmain", "TestBroken", failed),
		},
	)
	tr := DeriveTestRun(report)
	if got := tr.Outcomes["example.com/m/ok.TestOk"]; got != verify.TestPassed {
		t.Errorf("healthy package pass = %v, want a granted witness outcome", got)
	}
	for _, key := range []string{"example.com/m/redmain.TestGreen", "example.com/m/redmain.TestGreen/sub"} {
		if got, ok := tr.Outcomes[key]; ok {
			t.Errorf("%s = %v inside a red package, want no outcome: a red suite never yields green evidence", key, got)
		}
	}
	if got := tr.Outcomes["example.com/m/redmain.TestBroken"]; got != verify.TestFailed {
		t.Errorf("red result = %v, want FAILED surfaced regardless of package health", got)
	}
	if SuiteHealthy(report) {
		t.Error("suite with a red package reads healthy")
	}
}

// TestDeriveWitnessRequiresRaceInvocation pins the race gate: a healthy
// pass from a non-race invocation grants no witness outcome, while the
// invocation contributes suite health exactly like any other and its red
// results still surface. The resolved configuration carries the race
// rigor flag, so the report itself is the source of the attribution.
func TestDeriveWitnessRequiresRaceInvocation(t *testing.T) {
	stipulate.Covers(t, "REQ-go-race", "REQ-evidence-run-attributes")
	report := synthReport(
		[]*stipulatorv1.InvocationHealth{
			synthInvocation("race", true, map[string]stipulatorv1.HealthDisposition{"example.com/m/a": healthy}),
			synthInvocation("plain", false, map[string]stipulatorv1.HealthDisposition{"example.com/m/b": healthy}),
		},
		[]*stipulatorv1.TestResult{
			synthRow("race", "example.com/m/a", "TestRaced", passed),
			synthRow("plain", "example.com/m/b", "TestUnraced", passed),
			synthRow("plain", "example.com/m/b", "TestSkippy", skipped),
		},
	)
	tr := DeriveTestRun(report)
	if got := tr.Outcomes["example.com/m/a.TestRaced"]; got != verify.TestPassed {
		t.Errorf("race-invocation pass = %v, want a granted witness outcome", got)
	}
	if got, ok := tr.Outcomes["example.com/m/b.TestUnraced"]; ok {
		t.Errorf("non-race pass = %v, want no witness outcome (REQ-go-race)", got)
	}
	if got := tr.Outcomes["example.com/m/b.TestSkippy"]; got != verify.TestSkipped {
		t.Errorf("skip = %v, want SKIPPED: a skip grants nothing without reading as broken", got)
	}
	if !SuiteHealthy(report) {
		t.Error("healthy non-race invocation must contribute suite health like any other")
	}
	if !tr.RaceEnabled {
		t.Error("every granted witness derives from a race invocation; the run's rigor attribute must say so")
	}
	// The executor wires the normalized race flag onto the resolved
	// configuration, making the report self-contained about rigor.
	if !resolvedConfig(&NormalizedInvocation{Race: true}).GetRace() || resolvedConfig(&NormalizedInvocation{}).GetRace() {
		t.Error("resolved configuration does not carry the invocation's race flag")
	}
}

// TestDeriveShadowedTestGrantsNothing pins the shadowed-sibling shape: a
// test with no result in the report — its package aborted before the test
// finished or started — has no outcome key, which binding verification
// reads as unwitnessed, and no health or evidence is invented for it.
func TestDeriveShadowedTestGrantsNothing(t *testing.T) {
	stipulate.Covers(t, "REQ-go-witness")
	report := synthReport(
		[]*stipulatorv1.InvocationHealth{synthInvocation("race", true, map[string]stipulatorv1.HealthDisposition{
			"example.com/m/killer": testFailed,
		})},
		// The aborting package produced no row for the shadowed test.
		nil,
	)
	tr := DeriveTestRun(report)
	if got, ok := tr.Outcomes["example.com/m/killer.TestShadowed"]; ok {
		t.Errorf("shadowed test gained outcome %v with no result in the report", got)
	}
	if len(tr.Outcomes) != 0 {
		t.Errorf("outcomes invented for an abort-only report: %v", tr.Outcomes)
	}
}

// TestDeriveWorstOutcomeWins pins occurrence merging: when one test name
// carries several results (an invocation running with -count above one),
// a single red occurrence beats any green one, and the merge does not
// depend on result order.
func TestDeriveWorstOutcomeWins(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness")
	inv := synthInvocation("race", true, map[string]stipulatorv1.HealthDisposition{"example.com/m/a": testFailed})
	rows := []*stipulatorv1.TestResult{
		synthRow("race", "example.com/m/a", "TestFlaky", passed),
		synthRow("race", "example.com/m/a", "TestFlaky", failed),
	}
	forward := DeriveTestRun(synthReport([]*stipulatorv1.InvocationHealth{inv}, rows))
	slices.Reverse(rows)
	backward := DeriveTestRun(synthReport([]*stipulatorv1.InvocationHealth{inv}, rows))
	if forward.Outcomes["example.com/m/a.TestFlaky"] != verify.TestFailed ||
		backward.Outcomes["example.com/m/a.TestFlaky"] != verify.TestFailed {
		t.Errorf("flaky merge = %v / %v, want FAILED regardless of order",
			forward.Outcomes["example.com/m/a.TestFlaky"], backward.Outcomes["example.com/m/a.TestFlaky"])
	}
}

// TestDeriveFailureOutputRetained pins diagnosability: a test-scoped
// failure diagnostic from the execution rides the derived run keyed like
// its outcome, so a red witness is explained by the run that saw it.
func TestDeriveFailureOutputRetained(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness")
	report := synthReport(
		[]*stipulatorv1.InvocationHealth{synthInvocation("race", true, map[string]stipulatorv1.HealthDisposition{"example.com/m/a": testFailed})},
		[]*stipulatorv1.TestResult{synthRow("race", "example.com/m/a", "TestRed", failed)},
	)
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetInvocation("race")
	d.SetPackage("example.com/m/a")
	d.SetTest("TestRed")
	d.SetDisposition(testFailed)
	d.SetOutput("assertion output")
	report.SetDiagnostics([]*stipulatorv1.FailureDiagnostic{d})
	tr := DeriveTestRun(report)
	if got := tr.Failures["example.com/m/a.TestRed"]; got != "assertion output" {
		t.Errorf("failure output = %q, want the retained diagnostic", got)
	}
}

// TestDeriveRegistrationsCrossCheckedAgainstBindings pins the derived
// registration flow end to end: runtime registrations from the execution
// report ride the derived run into binding verification, where a
// registration naming a requirement with no tests- or proves-role binding
// is a verification error, a backed registration is reported with its
// outcome, and a bound test with no outcome in the witnessed run reads as
// unwitnessed.
func TestDeriveRegistrationsCrossCheckedAgainstBindings(t *testing.T) {
	stipulate.Covers(t, "REQ-go-covers-crosscheck", "REQ-go-covers", "REQ-go-witness")
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte("# T\n\n**REQ-d-a** (behavior): It MUST x.\n\n**REQ-d-b** (behavior): It MUST y.\n")},
		".stipulator/bindings/x.textproto": {Data: []byte(
			"bindings {\n  requirement_id: \"REQ-d-a\"\n  backend: \"go\"\n  symbol: \"example.com/m/a.TestBacked\"\n  role: BINDING_ROLE_TESTS\n}\n" +
				"bindings {\n  requirement_id: \"REQ-d-b\"\n  backend: \"go\"\n  symbol: \"example.com/m/a.TestShadowed\"\n  role: BINDING_ROLE_TESTS\n}\n")},
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	report := synthReport(
		[]*stipulatorv1.InvocationHealth{synthInvocation("race", true, map[string]stipulatorv1.HealthDisposition{"example.com/m/a": healthy})},
		[]*stipulatorv1.TestResult{
			synthRow("race", "example.com/m/a", "TestBacked", passed, "REQ-d-a"),
			synthRow("race", "example.com/m/a", "TestRogue", passed, "REQ-d-b"),
		},
	)
	tr := DeriveTestRun(report)
	rep := verify.Run(spec, store, nil, tr)

	rogue := false
	for _, p := range rep.Problems {
		if strings.Contains(p.Message, "TestRogue covers REQ-d-b") && strings.Contains(p.Message, "no tests- or proves-role binding backs it") {
			rogue = true
		}
	}
	if !rogue {
		t.Errorf("registration without a backing binding did not fail verification: %v", rep.Problems)
	}
	backed := false
	for _, reg := range rep.Registrations {
		if reg.Test == "TestBacked" && reg.Requirement == "REQ-d-a" && reg.Outcome == verify.TestPassed {
			backed = true
		}
	}
	if !backed {
		t.Errorf("backed registration not reported with its outcome: %+v", rep.Registrations)
	}
	// The bound TestShadowed produced no outcome in this witnessed run:
	// unwitnessed, counted as not-run, which coverage reads as broken.
	if rep.TestsNotRun != 1 {
		t.Errorf("TestsNotRun = %d, want the shadowed bound test counted unwitnessed", rep.TestsNotRun)
	}
}

// TestDeriveCachedOutcomeGrantsNoHealthOrEvidence pins the cache
// separation: with a witness cache full of green records for a test whose
// package is now red, the derivation still reads the suite red and grants
// the test nothing — health and evidence come only from the current
// execution of the producing invocation, and the cache is structurally
// not an input to either.
func TestDeriveCachedOutcomeGrantsNoHealthOrEvidence(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-evidence-freshness-no-health")
	tmp := t.TempDir()
	// The seeded record is structurally valid — it would load and serve on
	// a freshness-serving path — so ignoring it here is the derivation's
	// choice, not a loader rejection.
	if err := witnesscache.Install(tmp, witnesscache.Record{
		Package: "example.com/m/redmain",
		Test:    "TestGreen",
		Fingerprint: witnesscache.Fingerprint{
			MaximalClosure: "00112233445566778899aabbccddeeff",
			Toolchain:      "go1.26",
			BuildConfig:    "00112233445566778899aabbccddeeff",
			RuntimeInputs:  "eyJ2IjoxfQ",
			RuntimeDigest:  "00112233445566778899aabbccddeeff",
			ResultKind:     gofresh.CodeResult,
		},
		Outcomes: map[string]string{"example.com/m/redmain.TestGreen": "passed"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(witnesscache.Load(tmp)) != 1 {
		t.Fatal("seeded cache record is not loadable; the seed would prove nothing")
	}
	report := synthReport(
		[]*stipulatorv1.InvocationHealth{synthInvocation("race", true, map[string]stipulatorv1.HealthDisposition{
			"example.com/m/redmain": testFailed,
		})},
		[]*stipulatorv1.TestResult{synthRow("race", "example.com/m/redmain", "TestGreen", passed)},
	)
	recorder := &WitnessRecorder{dir: tmp}
	tr, err := recorder.Derive(context.Background(), report, nil)
	if err != nil {
		t.Fatal(err)
	}
	if SuiteHealthy(report) {
		t.Error("cached green beside a red TestMain read healthy; health must come from current execution alone")
	}
	if got, ok := tr.Outcomes["example.com/m/redmain.TestGreen"]; ok {
		t.Errorf("cached green surfaced as %v; serving could at most grant the served test, never ride a red execution", got)
	}
	if tr.Fresh != 0 {
		t.Errorf("health-judged execution served %d outcomes; an invocation whose health the run judges executes whole", tr.Fresh)
	}
	if tr.Uncached != tr.Ran {
		t.Errorf("uncacheable count = %d, want every executed test (%d) visible as uncacheable", tr.Uncached, tr.Ran)
	}
}

// TestDeriveDeterminismProperty quantifies the derivation over generated
// reports: deriving twice yields identical evidence, suite health is
// exactly the all-invocations-healthy conjunction, and every granted pass
// is backed by a healthy producing package inside a race-enabled
// invocation while red results always surface.
func TestDeriveDeterminismProperty(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness", "REQ-go-race")
	dispositions := []stipulatorv1.HealthDisposition{
		healthy, testFailed,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT,
	}
	outcomes := []stipulatorv1.TestOutcome{passed, failed, skipped}
	rapid.Check(t, func(t *rapid.T) {
		nInv := rapid.IntRange(0, 3).Draw(t, "invocations")
		var invocations []*stipulatorv1.InvocationHealth
		race := map[string]bool{}
		healthyPkg := map[string]bool{}
		for i := 0; i < nInv; i++ {
			name := string(rune('a' + i))
			r := rapid.Bool().Draw(t, name+"-race")
			race[name] = r
			pkgs := map[string]stipulatorv1.HealthDisposition{}
			for _, pkg := range rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"p", "q", "r"}), 0, 3, rapid.ID).Draw(t, name+"-pkgs") {
				d := rapid.SampledFrom(dispositions).Draw(t, name+"-"+pkg)
				pkgs["example.com/"+pkg] = d
				if d == healthy {
					healthyPkg[name+"\x00example.com/"+pkg] = true
				}
			}
			invocations = append(invocations, synthInvocation(name, r, pkgs))
		}
		nRows := rapid.IntRange(0, 8).Draw(t, "rows")
		var rows []*stipulatorv1.TestResult
		for i := 0; i < nRows; i++ {
			inv := rapid.SampledFrom([]string{"a", "b", "c", "ghost"}).Draw(t, "row-inv")
			pkg := "example.com/" + rapid.SampledFrom([]string{"p", "q", "r"}).Draw(t, "row-pkg")
			test := rapid.SampledFrom([]string{"TestA", "TestA/sub", "TestB", "FuzzC"}).Draw(t, "row-test")
			rows = append(rows, synthRow(inv, pkg, test, rapid.SampledFrom(outcomes).Draw(t, "row-outcome")))
		}
		report := synthReport(invocations, rows)

		first := DeriveTestRun(report)
		second := DeriveTestRun(report)
		if !maps.Equal(first.Outcomes, second.Outcomes) || !slices.Equal(first.Registrations, second.Registrations) ||
			first.Ran != second.Ran || first.RaceEnabled != second.RaceEnabled {
			t.Fatalf("derivation is not deterministic: %+v vs %+v", first, second)
		}

		wantHealthy := nInv > 0
		for _, h := range invocations {
			if h.GetDisposition() != healthy {
				wantHealthy = false
			}
		}
		if SuiteHealthy(report) != wantHealthy {
			t.Fatalf("SuiteHealthy = %v, want %v: health is the all-invocations conjunction and nothing else", SuiteHealthy(report), wantHealthy)
		}

		// Oracle: recompute each key's expected outcome independently.
		rank := map[verify.TestOutcome]int{verify.TestNotRun: 0, verify.TestSkipped: 1, verify.TestPassed: 2, verify.TestFailed: 3}
		expected := map[string]verify.TestOutcome{}
		for _, row := range rows {
			key := row.GetPackage() + "." + row.GetTest()
			inv := row.GetProducer().GetInvocation()
			o := verify.TestNotRun
			switch row.GetOutcome() {
			case failed:
				o = verify.TestFailed
			case skipped:
				o = verify.TestSkipped
			case passed:
				if race[inv] && healthyPkg[inv+"\x00"+row.GetPackage()] {
					o = verify.TestPassed
				}
			}
			if rank[o] > rank[expected[key]] {
				expected[key] = o
			}
		}
		for key, want := range expected {
			got, ok := first.Outcomes[key]
			if want == verify.TestNotRun {
				if ok {
					t.Fatalf("%s = %v, want no outcome: a pass without a healthy race-enabled producer grants nothing", key, got)
				}
				continue
			}
			if got != want {
				t.Fatalf("%s = %v, want %v", key, got, want)
			}
		}
		for key := range first.Outcomes {
			if expected[key] == verify.TestNotRun {
				t.Fatalf("outcome invented for %s", key)
			}
		}
	})
}

// TestDeriveSuiteHealthRejectsEmptyReport pins the guard against vacuous
// health: a report with no invocations is never healthy — an empty
// execution proves nothing.
func TestDeriveSuiteHealthRejectsEmptyReport(t *testing.T) {
	if SuiteHealthy(&stipulatorv1.ExecutionReport{}) {
		t.Error("empty execution report read healthy")
	}
}

// requireCacheAbsent asserts the corpus's witness store holds nothing.
func requireCacheAbsent(t *testing.T, dir string) {
	t.Helper()
	store, err := witnesscache.StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(store)
	if !os.IsNotExist(err) && len(entries) > 0 {
		t.Fatalf("witness store holds %d entries: %v", len(entries), err)
	}
}

// TestDeriveNamesUncacheableWithoutGroups pins the no-capture-group and
// degraded branches of per-test attribution: every executed top-level
// test carries the branch's reason, keyed correctly through
// multi-segment import paths — never a bare count.
func TestDeriveNamesUncacheableWithoutGroups(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	report := synthReport(
		[]*stipulatorv1.InvocationHealth{synthInvocation("plain", false, map[string]stipulatorv1.HealthDisposition{
			"example.com/m/deep/pkg": healthy,
		})},
		[]*stipulatorv1.TestResult{synthRow("plain", "example.com/m/deep/pkg", "TestOk", passed)},
	)
	recorder := &WitnessRecorder{dir: t.TempDir()}
	tr, err := recorder.Derive(context.Background(), report, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Uncached != 1 {
		t.Fatalf("uncached=%d, want 1", tr.Uncached)
	}
	why := tr.UncacheableReasons["example.com/m/deep/pkg.TestOk"]
	if !strings.Contains(why, "no capture group") {
		t.Fatalf("no-groups reason = %q (map %v), want the branch named per test", why, tr.UncacheableReasons)
	}

	degradedRecorder := &WitnessRecorder{dir: t.TempDir(), degraded: "engine fault"}
	tr, err = degradedRecorder.Derive(context.Background(), report, nil)
	if err != nil {
		t.Fatal(err)
	}
	why = tr.UncacheableReasons["example.com/m/deep/pkg.TestOk"]
	if !strings.Contains(why, "freshness path degraded: engine fault") {
		t.Fatalf("degraded reason = %q, want the fault named per test", why)
	}
}

// TestGrantingRunNamesRefusals pins the granting ladder's distinct
// refusals: an unhealthy process, an unproven testlog flush, and a
// subject with no terminal event each name their own leg.
func TestGrantingRunNamesRefusals(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	subject := gofresh.Subject{Package: "example.com/m/pkg", Symbol: "TestX"}
	row := synthRow("race", "example.com/m/pkg", "TestX", passed)
	key := keyOfProducer(row.GetProducer())

	unhealthy := newExecMerge()
	unhealthy.rows = append(unhealthy.rows, row)
	unhealthy.disp[key] = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED
	if _, why := grantingRun(subject, unhealthy); !strings.Contains(why, "no healthy process") {
		t.Errorf("unhealthy refusal = %q", why)
	}

	unproven := newExecMerge()
	unproven.rows = append(unproven.rows, row)
	unproven.disp[key] = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY
	if _, why := grantingRun(subject, unproven); !strings.Contains(why, "testlog flush unproven") {
		t.Errorf("unproven refusal = %q", why)
	}

	empty := newExecMerge()
	if _, why := grantingRun(subject, empty); !strings.Contains(why, "no process produced") {
		t.Errorf("no-terminal refusal = %q", why)
	}
}
