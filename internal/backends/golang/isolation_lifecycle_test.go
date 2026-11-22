package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/trace"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
	"pgregory.net/rapid"
)

// TestRepoPolicyPartitionConservesObligationUniverse pins the repository's own
// .stipulator/policy.textproto core/leaf split: every obligation in the
// workspace universe is selected once with zero omissions or duplicates, and
// race:core isolates the two heavy packages so internal/backends/golang is
// never throttled to narrow width behind dozens of fast leaf packages.
func TestRepoPolicyPartitionConservesObligationUniverse(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-conservation", "REQ-policy-record-location")
	if testing.Short() {
		t.Skip("discovers the repository's workspace obligation universe")
	}
	neutralAmbient(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	pc, err := LoadCapture(t.Context(), repoRoot)
	if err != nil {
		t.Fatalf("LoadCapture(%s): %v", repoRoot, err)
	}
	if err := pc.ConfigureExecution(true, nil); err != nil {
		t.Fatal(err)
	}
	universe, err := pc.ObligationUniverse(t.Context())
	if err != nil {
		t.Fatalf("ObligationUniverse: %v", err)
	}
	d, err := pc.discover(t.Context())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	var names []string
	var corePkgs []string
	for _, ic := range d.invocations {
		names = append(names, ic.n.Name)
		if ic.n.Name == "race:core" {
			for _, ob := range ic.obligations {
				if ob.Kind == ObligationPackage {
					corePkgs = append(corePkgs, ob.Package)
				}
			}
		}
	}
	wantNames := []string{"race:core", "race:leaf", "race:stipulate", "race:stipulate/structural"}
	if !slices.Equal(names, wantNames) {
		t.Fatalf("policy invocations = %v, want %v", names, wantNames)
	}
	slices.Sort(corePkgs)
	wantCore := []string{
		"github.com/greatliontech/stipulator/internal/backends/golang",
		"github.com/greatliontech/stipulator/internal/cmd",
	}
	if !slices.Equal(corePkgs, wantCore) {
		t.Fatalf("race:core packages = %v, want %v", corePkgs, wantCore)
	}
	if findings := PartitionReports(universe, d.selections()); len(findings) != 0 {
		t.Fatalf("policy partition findings = %v, want zero uncovered/duplicate obligations", findings)
	}
}

// TestReducedBatchDecisionProperty checks deniedTests and
// shouldRetryReducedBatch against an independent set-theoretic oracle across
// generated dispositions, abort rosters, test outcomes, and selections.
func TestReducedBatchDecisionProperty(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-isolation")
	allNames := []string{"TestA", "TestB", "TestC", "TestD", "TestE", "TestF"}
	dispositions := []stipulatorv1.HealthDisposition{
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_UNSPECIFIED,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT,
	}
	outcomes := []stipulatorv1.TestOutcome{
		stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED,
		stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED,
		stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED,
	}
	rapid.Check(t, func(rt *rapid.T) {
		disp := rapid.SampledFrom(dispositions).Draw(rt, "disp")
		explicitSel := rapid.Bool().Draw(rt, "explicit_sel")
		var selected []string
		if explicitSel {
			for _, name := range allNames {
				if rapid.Bool().Draw(rt, "sel_"+name) {
					selected = append(selected, name)
				}
			}
		}
		candidates := allNames
		if explicitSel {
			candidates = selected
		}
		var aborted []string
		var rows []*stipulatorv1.TestResult
		oraclePassed := map[string]bool{}
		oracleFailed := map[string]bool{}
		oracleTerminal := map[string]bool{}
		oracleAborted := map[string]bool{}
		for _, name := range candidates {
			mode := rapid.IntRange(0, 3).Draw(rt, "mode_"+name)
			switch mode {
			case 0: // unreached
			case 1: // aborted (started, unfinished)
				sub := rapid.Bool().Draw(rt, "abort_sub_"+name)
				if sub {
					aborted = append(aborted, name+"/sub")
				} else {
					aborted = append(aborted, name)
				}
				oracleAborted[name] = true
			case 2, 3: // terminal top-level outcome (+ optional subtest row)
				out := rapid.SampledFrom(outcomes).Draw(rt, "out_"+name)
				if rapid.Bool().Draw(rt, "sub_"+name) {
					subRow := &stipulatorv1.TestResult{}
					subRow.SetPackage("example.com/p")
					subRow.SetTest(name + "/child")
					subRow.SetOutcome(rapid.SampledFrom(outcomes).Draw(rt, "subout_"+name))
					rows = append(rows, subRow)
				}
				tr := &stipulatorv1.TestResult{}
				tr.SetPackage("example.com/p")
				tr.SetTest(name)
				tr.SetOutcome(out)
				rows = append(rows, tr)
				oracleTerminal[name] = true
				switch out {
				case stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED:
					oraclePassed[name] = true
				case stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED:
					oracleFailed[name] = true
				}
			}
		}
		run := &packageRun{pkg: "example.com/p", disposition: disp, aborted: aborted, tests: rows}
		gotDenied := deniedTests(run, selected)

		isolatesDisp := disp == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED ||
			disp == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED ||
			disp == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT
		var wantDenied []string
		if isolatesDisp {
			wantSet := map[string]bool{}
			for name := range oracleAborted {
				wantSet[name] = true
			}
			for name := range oraclePassed {
				wantSet[name] = true
			}
			for _, name := range selected {
				if !oracleTerminal[name] {
					wantSet[name] = true
				}
			}
			for name := range oracleFailed {
				delete(wantSet, name)
			}
			for name := range wantSet {
				wantDenied = append(wantDenied, name)
			}
			slices.Sort(wantDenied)
		}
		if !slices.Equal(gotDenied, wantDenied) {
			rt.Fatalf("deniedTests = %v, want %v (disp=%v selected=%v)", gotDenied, wantDenied, disp, selected)
		}

		gotRetry := shouldRetryReducedBatch(run, gotDenied)
		wantRetry := len(gotDenied) >= 2 &&
			disp == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED &&
			len(aborted) == 0 &&
			len(oracleFailed) > 0
		if gotRetry != wantRetry {
			rt.Fatalf("shouldRetryReducedBatch = %v, want %v (disp=%v aborted=%v failed=%v denied=%v)", gotRetry, wantRetry, disp, aborted, oracleFailed, gotDenied)
		}
		if gotRetry && explicitSel && len(gotDenied) >= len(selected) {
			rt.Fatalf("reduced batch %v is not strictly narrower than explicit selection %v", gotDenied, selected)
		}
	})

	// Deterministic boundary matrix pinning rare corners (including subtest-only
	// FAILED rows without a top-level FAILED row, single-denied threshold, and
	// non-empty abort rosters alongside a top-level failure).
	row := func(test string, out stipulatorv1.TestOutcome) *stipulatorv1.TestResult {
		tr := &stipulatorv1.TestResult{}
		tr.SetPackage("example.com/p")
		tr.SetTest(test)
		tr.SetOutcome(out)
		return tr
	}
	for _, bc := range []struct {
		name       string
		run        packageRun
		selected   []string
		wantDenied []string
		wantRetry  bool
	}{
		{
			name: "subtest-only failure without top-level failure refuses reduced batch",
			run: packageRun{
				pkg:         "example.com/p",
				disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
				tests: []*stipulatorv1.TestResult{
					row("TestA/sub", stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED),
					row("TestA", stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED),
					row("TestB", stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED),
				},
			},
			selected:   []string{"TestA", "TestB"},
			wantDenied: []string{"TestA", "TestB"},
			wantRetry:  false,
		},
		{
			name: "single denied test with top-level failure skips reduced batch",
			run: packageRun{
				pkg:         "example.com/p",
				disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
				tests: []*stipulatorv1.TestResult{
					row("TestA", stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED),
					row("TestB", stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED),
				},
			},
			selected:   []string{"TestA", "TestB"},
			wantDenied: []string{"TestA"},
			wantRetry:  false,
		},
		{
			name: "two denied tests with top-level failure and empty aborted admits reduced batch",
			run: packageRun{
				pkg:         "example.com/p",
				disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
				tests: []*stipulatorv1.TestResult{
					row("TestA", stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED),
					row("TestB", stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED),
					row("TestC", stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED),
				},
			},
			selected:   []string{"TestA", "TestB", "TestC"},
			wantDenied: []string{"TestA", "TestB"},
			wantRetry:  true,
		},
		{
			name: "aborted sibling alongside top-level failure refuses reduced batch",
			run: packageRun{
				pkg:         "example.com/p",
				disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
				aborted:     []string{"TestD"},
				tests: []*stipulatorv1.TestResult{
					row("TestA", stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED),
					row("TestB", stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED),
					row("TestC", stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED),
				},
			},
			selected:   []string{"TestA", "TestB", "TestC", "TestD"},
			wantDenied: []string{"TestA", "TestB", "TestD"},
			wantRetry:  false,
		},
	} {
		t.Run(bc.name, func(t *testing.T) {
			gotDenied := deniedTests(&bc.run, bc.selected)
			if !slices.Equal(gotDenied, bc.wantDenied) {
				t.Fatalf("deniedTests = %v, want %v", gotDenied, bc.wantDenied)
			}
			if gotRetry := shouldRetryReducedBatch(&bc.run, gotDenied); gotRetry != bc.wantRetry {
				t.Fatalf("shouldRetryReducedBatch = %v, want %v", gotRetry, bc.wantRetry)
			}
		})
	}
}

func isolateTempDir(t *testing.T) string {
	t.Helper()
	isolatedTmp := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, isolatedTmp)
	}
	return isolatedTmp
}

// TestReducedBatchIsolationRecoversCleanFailuresAndFallsBackToSolo verifies:
//  1. Clean top-level failures in a multi-test batch recover all N >= 2
//     passing siblings in a single reduced-batch process (zero per-test solo
//     processes), closing both attempts independently in afterSlot, unlinking
//     both testlogs, and publishing valid reusable witnesscache records for
//     the passing siblings.
//  2. If the reduced batch itself fails on an order-dependent test, that new
//     failure stands and only the remaining non-failing tests fall back to
//     solo isolation.
//  3. Mid-run aborts (len(aborted) > 0) bypass the reduced batch and go
//     directly to per-test solo isolation.
func TestReducedBatchIsolationRecoversCleanFailuresAndFallsBackToSolo(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-isolation", "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("compiles and executes selective isolation witness fixtures")
	}
	neutralAmbient(t)

	t.Run("clean failure recovers passing siblings in one reduced batch and unlinks testlogs", func(t *testing.T) {
		isolatedTmp := isolateTempDir(t)
		dir := writeModule(t, map[string]string{
			"go.mod": "module example.com/redbatch\n\ngo 1.26\n",
			"p/p_test.go": `package p

import "testing"

func TestPass1(t *testing.T) {}
func TestPass2(t *testing.T) {}
func TestPass3(t *testing.T) {}
func TestFail1(t *testing.T) { t.Fatal("clean failure 1") }
func TestFail2(t *testing.T) { t.Fatal("clean failure 2") }
`,
		})
		writeRacePolicy(t, dir)
		rec := trace.NewRecorder()
		ctx := trace.WithSink(t.Context(), rec)
		cold, err := RunWitnesses(ctx, dir, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		for _, pass := range []string{"TestPass1", "TestPass2", "TestPass3"} {
			if got := cold.Outcomes["example.com/redbatch/p."+pass]; got != verify.TestPassed {
				t.Fatalf("%s outcome = %v, want TestPassed", pass, got)
			}
		}
		for _, fail := range []string{"TestFail1", "TestFail2"} {
			if got := cold.Outcomes["example.com/redbatch/p."+fail]; got != verify.TestFailed {
				t.Fatalf("%s outcome = %v, want TestFailed", fail, got)
			}
		}
		if cold.Uncached != 2 {
			t.Fatalf("Uncached = %d, want 2 (only the two failing tests): %v", cold.Uncached, cold.UncacheableReasons)
		}
		if plans := rec.ByName("reduced_batch_plan"); len(plans) != 1 {
			t.Fatalf("reduced_batch_plan events = %d, want 1: %+v", len(plans), plans)
		}
		if solos := rec.ByName("solo_exec"); len(solos) != 0 {
			t.Fatalf("solo_exec events = %d, want 0 (all 3 passes recovered in 1 reduced batch): %+v", len(solos), solos)
		}
		if finishes := rec.ByName("finish_native_run"); len(finishes) != 2 {
			t.Fatalf("finish_native_run events = %d, want 2 (initial batch + reduced batch): %+v", len(finishes), finishes)
		}
		entries, err := os.ReadDir(isolatedTmp)
		if err != nil {
			t.Fatal(err)
		}
		for _, ent := range entries {
			if strings.HasPrefix(ent.Name(), "stipulator-testlog-") {
				t.Fatalf("leaked stipulator-testlog-* file after RunWitnesses: %s", ent.Name())
			}
		}
		records := witnesscache.Load(t.Context(), dir).Records
		if len(records) != 3 {
			t.Fatalf("published witnesscache records = %d, want 3: %+v", len(records), records)
		}

		// Warm rerun: the 3 passing siblings serve from witnesscache; only the
		// 2 failing tests execute.
		warm, err := RunWitnesses(t.Context(), dir, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		if warm.Fresh != 3 || warm.Ran != 2 {
			t.Fatalf("warm rerun: fresh=%d ran=%d, want 3 fresh / 2 ran", warm.Fresh, warm.Ran)
		}
	})

	t.Run("secondary failure in reduced batch falls back to solo for remaining passes", func(t *testing.T) {
		dir := writeModule(t, map[string]string{
			"go.mod": "module example.com/redfallback\n\ngo 1.26\n",
			"p/p_test.go": `package p

import (
	"flag"
	"strings"
	"testing"
)

func TestFailInitial(t *testing.T) { t.Fatal("initial failure") }

func TestFailsInReducedBatch(t *testing.T) {
	runFlag := flag.Lookup("test.run").Value.String()
	// Passes in the full batch (which includes TestFailInitial), but fails
	// when TestFailInitial is excluded in the reduced batch.
	if !strings.Contains(runFlag, "TestFailInitial") {
		t.Fatal("order-dependent failure in reduced batch")
	}
}

func TestSurvivorA(t *testing.T) {}
func TestSurvivorB(t *testing.T) {}
`,
		})
		writeRacePolicy(t, dir)
		rec := trace.NewRecorder()
		ctx := trace.WithSink(t.Context(), rec)
		tr, err := RunWitnesses(ctx, dir, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		for _, fail := range []string{"TestFailInitial", "TestFailsInReducedBatch"} {
			if got := tr.Outcomes["example.com/redfallback/p."+fail]; got != verify.TestFailed {
				t.Fatalf("%s outcome = %v, want TestFailed", fail, got)
			}
		}
		for _, pass := range []string{"TestSurvivorA", "TestSurvivorB"} {
			if got := tr.Outcomes["example.com/redfallback/p."+pass]; got != verify.TestPassed {
				t.Fatalf("%s outcome = %v, want TestPassed", pass, got)
			}
		}
		if plans := rec.ByName("reduced_batch_plan"); len(plans) != 1 {
			t.Fatalf("reduced_batch_plan events = %d, want 1", len(plans))
		}
		solos := rec.ByName("solo_exec")
		if len(solos) != 2 {
			t.Fatalf("solo_exec events = %d, want 2 (only TestSurvivorA and TestSurvivorB): %+v", len(solos), solos)
		}
		if len(witnesscache.Load(t.Context(), dir).Records) != 2 {
			t.Fatalf("published witnesscache records != 2")
		}
	})

	t.Run("mid-run abort skips reduced batch and isolates solo directly", func(t *testing.T) {
		dir := writeModule(t, map[string]string{
			"go.mod": "module example.com/abortbatch\n\ngo 1.26\n",
			"p/p_test.go": `package p

import (
	"os"
	"testing"
)

func TestCrash(t *testing.T)   { os.Exit(2) }
func TestBeside1(t *testing.T) {}
func TestBeside2(t *testing.T) {}
`,
		})
		writeRacePolicy(t, dir)
		rec := trace.NewRecorder()
		ctx := trace.WithSink(t.Context(), rec)
		tr, err := RunWitnesses(ctx, dir, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		if plans := rec.ByName("reduced_batch_plan"); len(plans) != 0 {
			t.Fatalf("mid-run abort triggered reduced_batch_plan: %+v", plans)
		}
		for _, pass := range []string{"TestBeside1", "TestBeside2"} {
			if got := tr.Outcomes["example.com/abortbatch/p."+pass]; got != verify.TestPassed {
				t.Fatalf("%s outcome = %v, want TestPassed", pass, got)
			}
		}
	})
}

// TestUnwitnessedAttemptOmitsTestlogAndSingleSubjectPrepTracesNeedsObservation
// pins:
//  1. Unwitnessed attempts (leg == nil) omit -test.testlogfile and create zero
//     stipulator-testlog-* temporary files.
//  2. Witnessed single-subject and multi-subject attempts emit
//     prepare_native_attempt trace provenance naming single_subject and
//     needs_observation accurately.
func TestUnwitnessedAttemptOmitsTestlogAndSingleSubjectPrepTracesNeedsObservation(t *testing.T) {
	stipulate.Covers(t, "REQ-go-retained-test-execution", "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("compiles and executes retained test binaries")
	}
	neutralAmbient(t)

	t.Run("unwitnessed leg omits testlogfile flag and temp file", func(t *testing.T) {
		isolatedTmp := isolateTempDir(t)
		n := retainedFixtureInvocation(t, map[string]string{"p_test.go": `package p

import (
	"os"
	"strings"
	"testing"
)

func TestNoTestlogFlag(t *testing.T) {
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "-test.testlogfile") {
			t.Fatalf("unwitnessed attempt received unexpected %q", arg)
		}
	}
}
`})
		if _, err := DiscoverInvocation(t.Context(), n); err != nil {
			t.Fatal(err)
		}
		res, err := executeSelectionObserved(t.Context(), n, TestSelection{"retained.test": {"TestNoTestlogFlag"}}, nil, nil, spawnOrdinals())
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Processes) != 1 || res.Processes[0].Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
			t.Fatalf("unwitnessed execution failed: %+v (diags: %v)", res.Processes, res.Diagnostics)
		}
		entries, err := os.ReadDir(isolatedTmp)
		if err != nil {
			t.Fatal(err)
		}
		for _, ent := range entries {
			if strings.HasPrefix(ent.Name(), "stipulator-testlog-") {
				t.Fatalf("unwitnessed execution created temp testlog %s", ent.Name())
			}
		}
	})

	t.Run("witnessed single and package attempts record needs_observation provenance", func(t *testing.T) {
		dir := writeModule(t, map[string]string{
			"go.mod":         "module example.com/preptrace\n\ngo 1.26\n",
			"pure/p_test.go": "package pure\n\nimport \"testing\"\n\nfunc TestPure(t *testing.T) {}\n",
			"obs/p_test.go":  "package obs\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestEnvA(t *testing.T) { _ = os.Getenv(\"PREP_TRACE_VAR\") }\nfunc TestEnvB(t *testing.T) {}\n",
		})
		writeRacePolicy(t, dir)
		rec := trace.NewRecorder()
		tr, err := RunWitnesses(trace.WithSink(t.Context(), rec), dir, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		if tr.Degraded != "" || tr.Ran != 3 || tr.Uncached != 0 {
			t.Fatalf("cold run = (degraded=%q, ran=%d, uncached=%d, reasons=%v), want 3 ran / 0 uncached", tr.Degraded, tr.Ran, tr.Uncached, tr.UncacheableReasons)
		}
		preps := rec.ByName("prepare_native_attempt")
		if len(preps) != 2 {
			t.Fatalf("prepare_native_attempt events = %d, want 2: %+v", len(preps), preps)
		}
		byPkg := map[string]trace.Event{}
		for _, ev := range preps {
			byPkg[ev.Target] = ev
		}
		pureEv := byPkg["example.com/preptrace/pure"]
		if single, ok := pureEv.BoolField("single_subject"); !ok || !single {
			t.Fatalf("pure single_subject = (%v, %v), want (true, true)", single, ok)
		}
		if needs, ok := pureEv.BoolField("needs_observation"); !ok || needs {
			t.Fatalf("pure needs_observation = (%v, %v), want (false, true)", needs, ok)
		}
		obsEv := byPkg["example.com/preptrace/obs"]
		if single, ok := obsEv.BoolField("single_subject"); !ok || single {
			t.Fatalf("obs single_subject = (%v, %v), want (false, true)", single, ok)
		}
		if needs, ok := obsEv.BoolField("needs_observation"); !ok || !needs {
			t.Fatalf("obs needs_observation = (%v, %v), want (true, true)", needs, ok)
		}
	})

	t.Run("witnessed missing testlog refuses unbracketed pure fastpath and incomplete retry native evidence", func(t *testing.T) {
		n := retainedFixtureInvocation(t, map[string]string{
			"p_test.go": "package p\n\nimport \"testing\"\n\n//gofresh:pure\nfunc TestPure(t *testing.T) {}\n",
		})
		n.Race = true
		if _, err := DiscoverInvocation(t.Context(), n); err != nil {
			t.Fatal(err)
		}
		if _, _, err := prepareNativeAttempt(t.Context(), n, "retained.test", &packageLeg{}, nil, nil, "", nil); err == nil || !strings.Contains(err.Error(), "requires a testlog capture path") {
			t.Fatalf("prepareNativeAttempt with empty logPath = %v, want testlog capture refusal", err)
		}
		legs := executionLegs(t.Context(), n, []string{"retained.test"})
		defer func() {
			for _, leg := range legs {
				leg.dispose()
			}
		}()
		leg := legs["retained.test"]
		if leg == nil {
			t.Fatal("expected witnessed leg for race-enabled retained.test")
		}
		built := buildRetained(t.Context(), n, "retained.test", retainedProcessHooks{})
		if built.Err != nil {
			t.Fatalf("buildRetained: %v", built.Err)
		}
		defer built.Artifact.Close()

		missingTmp := filepath.Join(t.TempDir(), "missing-tempdir")
		for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
			t.Setenv(key, missingTmp)
		}
		gate := newAdmission(t.Context(), 1)
		defer gate.leave()
		exec := &packageExecution{built: &built, directory: n.PkgDirs["retained.test"]}
		run := runNativePackage(t.Context(), t.Context(), n, "retained.test", []string{"TestPure"}, 1, gate, leg, exec)
		defer run.attempt.release()
		if err := finalizeRun(n, &run, false, ""); err != nil {
			t.Fatalf("finalizeRun: %v", err)
		}
		finishNativeRun(t.Context(), &run)
		if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
			t.Fatalf("run disposition = %v, want HEALTHY", run.disposition)
		}
		if run.obs == nil || run.obs.Wire.GetCompleted() != nil {
			t.Fatalf("missing testlog capture produced completed observation: %+v", run.obs)
		}
		if !strings.Contains(run.obs.Wire.GetIncompleteReason(), "creating testlog capture:") {
			t.Fatalf("incomplete reason = %q, want creating testlog capture error", run.obs.Wire.GetIncompleteReason())
		}

		subj := gofresh.Subject{Package: "retained.test", Symbol: "TestPure"}
		prod := &stipulatorv1.ProducerIdentity{}
		prod.SetInvocation(n.Name)
		prod.SetProcessId(1)
		prod.SetProcessOrdinal(1)
		row := &stipulatorv1.TestResult{}
		row.SetPackage("retained.test")
		row.SetTest("TestPure")
		row.SetOutcome(stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED)
		row.SetProducer(prod)
		synthObs := &ProcessObservation{
			Wire: &stipulatorv1.Observation{},
			final: gofresh.Finalized{
				Subjects: []gofresh.FinalizedSubject{{
					Subject: subj,
					Verdict: gofresh.Verdict{Status: gofresh.Valid},
				}},
			},
		}
		synthObs.Wire.SetPackage("retained.test")
		synthObs.Wire.SetProducer(prod)
		synthObs.Wire.SetIncompleteReason("synthetic incomplete observation")
		cands := collectRetryCandidates(&witnessGroup{}, packageUnit{
			pkg: "retained.test",
			run: packageRun{
				pkg:         "retained.test",
				disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
				tests:       []*stipulatorv1.TestResult{row},
				obs:         synthObs,
			},
		}, newExecMerge(), map[gofresh.Subject]uncacheable{})
		if len(cands) != 1 || cands[0].native != nil {
			t.Fatalf("collectRetryCandidates attached native evidence from incomplete observation: %+v", cands)
		}
	})

	t.Run("witness recorder close abort and explain release group session state", func(t *testing.T) {
		dir := writeModule(t, map[string]string{
			"go.mod":      "module example.com/sesslife\n\ngo 1.26\n",
			"p/p_test.go": "package p\n\nimport \"testing\"\n\nvar shared int\n\nfunc TestMut(t *testing.T) { shared++ }\nfunc TestRead(t *testing.T) { _ = shared }\n",
		})
		writeRacePolicy(t, dir)
		pc, err := LoadCapture(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := NewWitnessRecorder(t.Context(), pc, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		d, err := pc.discover(t.Context())
		if err != nil || len(d.groups) != 1 {
			t.Fatalf("discover groups = %d, err = %v", len(d.groups), err)
		}
		if d.groups[0].sessionState == nil || d.groups[0].legs == nil {
			t.Fatal("active WitnessRecorder did not populate group sessionState and legs")
		}
		rec.Close()
		if d.groups[0].sessionState != nil || d.groups[0].legs != nil || d.groups[0].view != nil {
			t.Fatalf("WitnessRecorder.Close retained group state: session=%v legs=%v view=%v", d.groups[0].sessionState, d.groups[0].legs, d.groups[0].view)
		}
		if _, _, err := ExplainDynamicState(t.Context(), pc, "example.com/sesslife/p", "shared"); err != nil {
			t.Fatal(err)
		}
		if d.groups[0].sessionState != nil {
			t.Fatalf("ExplainDynamicState retained group sessionState: %v", d.groups[0].sessionState)
		}
		cancelCtx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, _ = prepareWitnessGroups(cancelCtx, dir, d, nil)
		if d.groups[0].sessionState != nil {
			t.Fatalf("incomplete prepareWitnessGroups retained group sessionState: %v", d.groups[0].sessionState)
		}
	})
}

// TestMovedInputsAttributionSurfacesOnCloseAndCheckingVerdicts pins coherent
// MovedInputs formatting across RuntimeInputSpanError close refusals and
// CheckedCandidate verdict attributions, including truncation and idempotence.
func TestMovedInputsAttributionSurfacesOnCloseAndCheckingVerdicts(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-diagnosable")

	for _, tc := range []struct {
		name   string
		reason string
		movers []string
		want   string
	}{
		{
			name:   "no movers preserves reason",
			reason: "runtimeinputs",
			movers: nil,
			want:   "runtimeinputs",
		},
		{
			name:   "empty reason defaults to runtimeinputs",
			reason: "",
			movers: []string{"path a/data.txt"},
			want:   "runtimeinputs (moved: path a/data.txt)",
		},
		{
			name:   "four movers listed whole",
			reason: "runtimeinputs",
			movers: []string{"path a", "path b", "path c", "path d"},
			want:   "runtimeinputs (moved: path a, path b, path c, path d)",
		},
		{
			name:   "more than four movers capped with remainder count",
			reason: "runtimeinputs",
			movers: []string{"path a", "path b", "path c", "path d", "path e", "path f"},
			want:   "runtimeinputs (moved: path a, path b, path c, path d, +2 more)",
		},
		{
			name:   "already formatted reason is idempotent",
			reason: "runtimeinputs (moved: path a)",
			movers: []string{"path a"},
			want:   "runtimeinputs (moved: path a)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := verdictReasonWithMovers(tc.reason, tc.movers); got != tc.want {
				t.Fatalf("verdictReasonWithMovers(%q, %v) = %q, want %q", tc.reason, tc.movers, got, tc.want)
			}
		})
	}

	spanErr := &gofresh.RuntimeInputSpanError{
		Reason:      "observation bracket moved: mut",
		MovedInputs: []string{"path mut/testdata/input.txt"},
	}
	wrapped := fmt.Errorf("close: %w", spanErr)
	got := nativeCloseRefusal(wrapped)
	if got.class != reasonPostRun || !strings.Contains(got.detail, spanErr.Error()) || !strings.Contains(got.detail, "(moved: path mut/testdata/input.txt)") {
		t.Fatalf("nativeCloseRefusal(%v) = %+v, want post-run validation naming moved input", wrapped, got)
	}
	plainSpan := &gofresh.RuntimeInputSpanError{Reason: "observation bracket moved: mut"}
	if gotPlain := nativeCloseRefusal(plainSpan); gotPlain.class != reasonPostRun || strings.Contains(gotPlain.detail, "(moved: ") {
		t.Fatalf("empty MovedInputs added suffix: %+v", gotPlain)
	}
	if gotSource := nativeCloseRefusal(errors.New("other fault")); gotSource.class != reasonSourceFailed {
		t.Fatalf("non-movement error classified as %v, want reasonSourceFailed", gotSource.class)
	}
}
