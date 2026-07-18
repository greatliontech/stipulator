package golang

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

// writePolicyRecord commits a policy record at its fixed location under
// dir, so RunWitnesses exercises the real loading seam.
func writePolicyRecord(t *testing.T, dir string, p *stipulatorv1.TestPolicy) {
	t.Helper()
	raw, err := policy.Render(p)
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, filepath.FromSlash(policy.Path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGoRunWitnessesRequiresPolicyRecord pins the no-fallback rule: the
// selective witness runner consumes the committed test policy through the
// shared loading seam and surfaces every load failure — a record problem
// and an operational read fault alike — instead of assuming a universal
// suite.
//
//gofresh:pure
func TestGoRunWitnessesRequiresPolicyRecord(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/nopolicy\n\ngo 1.26\n",
	})
	if _, err := RunWitnesses(context.Background(), tmp); !errors.Is(err, policy.ErrRecord) {
		t.Errorf("missing policy record: err = %v, want policy.ErrRecord", err)
	}

	recordPath := filepath.Join(tmp, filepath.FromSlash(policy.Path))
	if err := os.MkdirAll(filepath.Dir(recordPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, []byte("not a policy record\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RunWitnesses(context.Background(), tmp); !errors.Is(err, policy.ErrRecord) {
		t.Errorf("invalid policy record: err = %v, want policy.ErrRecord", err)
	}

	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions cannot make the record unreadable")
	}
	if err := os.Chmod(recordPath, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(recordPath, 0o644)
	_, err := RunWitnesses(context.Background(), tmp)
	if err == nil {
		t.Fatal("unreadable policy record did not fail the run")
	}
	if errors.Is(err, policy.ErrRecord) {
		t.Errorf("operational read fault classified as a record problem: %v", err)
	}
}

// disjointWitnessFixture is the module the serve/execute/outside
// partition tests run: one race-covered package, one package no
// invocation covers, one only a non-race invocation covers, and one two
// race invocations cover. The outside packages' tests write sentinel
// files, so an execution that should never happen is a visible artifact.
func disjointWitnessFixture(t *testing.T) string {
	t.Helper()
	return writeModule(t, map[string]string{
		"go.mod":         "module example.com/wit\n\ngo 1.26\n",
		"covered/one.go": "package covered\n\nfunc One() string { return \"one-v1\" }\n",
		"covered/covered_test.go": `package covered

import "testing"

//gofresh:pure
func TestOne(t *testing.T) {
	t.Log("stipulator:covers REQ-wit-probe")
	if One() == "" {
		t.Fatal("empty")
	}
}
`,
		"covered2/two.go": "package covered2\n\nfunc Two() string { return \"two-v1\" }\n",
		"covered2/covered2_test.go": `package covered2

import "testing"

//gofresh:pure
func TestTwo(t *testing.T) {
	if Two() == "" {
		t.Fatal("empty")
	}
}
`,
		"uncovered/uncovered_test.go": `package uncovered

import (
	"os"
	"testing"
)

func TestNone(t *testing.T) {
	_ = os.WriteFile("ran.sentinel", nil, 0o644)
}
`,
		"nonrace/nonrace_test.go": `package nonrace

import (
	"os"
	"testing"
)

func TestPlain(t *testing.T) {
	_ = os.WriteFile("ran.sentinel", nil, 0o644)
}
`,
		"twice/twice_test.go": `package twice

import (
	"os"
	"testing"
)

func TestTwice(t *testing.T) {
	_ = os.WriteFile("ran.sentinel", nil, 0o644)
}
`,
	})
}

// requireNoSentinels asserts that no outside-policy package's test ever
// executed: an executed test would have left its sentinel file behind.
func requireNoSentinels(t *testing.T, tmp string) {
	t.Helper()
	for _, pkg := range []string{"uncovered", "nonrace", "twice"} {
		if _, err := os.Stat(filepath.Join(tmp, pkg, "ran.sentinel")); !os.IsNotExist(err) {
			t.Errorf("a process ran for outside-policy package %s: %v", pkg, err)
		}
	}
}

// TestGoRunWitnessesServeExecuteOutsideDisjoint pins the runner's
// partition: every expected witness subject is served, executed, or
// outside the policy — exactly one of the three — on the cold run, the
// warm run, and a partial-stale run alike. Outside-policy subjects — a
// package covered by no invocation, only by a non-race invocation, or by
// two invocations — neither serve nor execute and ride the result as a
// count; serving grants witness outcomes and registrations without any
// health judgment, and a stale subject re-executes while its still-valid
// sibling serves.
func TestGoRunWitnessesServeExecuteOutsideDisjoint(t *testing.T) {
	stipulate.Covers(t, "REQ-core-one-execution", "REQ-evidence-witness-freshness",
		"REQ-evidence-freshness-no-health")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := disjointWitnessFixture(t)
	race := &stipulatorv1.GoInvocationConfig{}
	race.SetPackages([]string{"./covered", "./covered2", "./twice"})
	race.SetRace(true)
	dup := &stipulatorv1.GoInvocationConfig{}
	dup.SetPackages([]string{"./twice"})
	dup.SetRace(true)
	plain := &stipulatorv1.GoInvocationConfig{}
	plain.SetPackages([]string{"./nonrace"})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("a-race", race),
		goInvocation("b-dup", dup),
		goInvocation("c-plain", plain),
	})
	writePolicyRecord(t, tmp, p)

	// Expected subjects: covered.TestOne, covered2.TestTwo, and the three
	// outside ones (TestNone uncovered, TestPlain non-race, TestTwice
	// doubly covered).
	const expectedTotal = 5
	requirePartition := func(phase string, tr *verify.TestRun, fresh, ran int) {
		t.Helper()
		if tr.Degraded != "" {
			t.Fatalf("%s: freshness path degraded: %s", phase, tr.Degraded)
		}
		if tr.Fresh != fresh || tr.Ran != ran || tr.OutsidePolicy != 3 {
			t.Errorf("%s: fresh=%d ran=%d outside=%d, want %d/%d/3",
				phase, tr.Fresh, tr.Ran, tr.OutsidePolicy, fresh, ran)
		}
		// The disjointness invariant: each expected subject counted
		// exactly once across served, executed, and outside-policy. The
		// count identity holds when every executed subject reaches a
		// terminal event, as here; a denied subject leaves every bucket
		// and its visibility is its package-keyed diagnostic (pinned by
		// the envelope-cutoff test).
		if tr.Fresh+tr.Ran+tr.OutsidePolicy != expectedTotal {
			t.Errorf("%s: fresh+ran+outside = %d, want %d: a subject was double-granted or dropped",
				phase, tr.Fresh+tr.Ran+tr.OutsidePolicy, expectedTotal)
		}
		for _, key := range []string{
			"example.com/wit/covered.TestOne",
			"example.com/wit/covered2.TestTwo",
		} {
			if got := tr.Outcomes[key]; got != verify.TestPassed {
				t.Errorf("%s: %s = %v, want PASSED", phase, key, got)
			}
		}
		for _, key := range []string{
			"example.com/wit/uncovered.TestNone",
			"example.com/wit/nonrace.TestPlain",
			"example.com/wit/twice.TestTwice",
		} {
			if got, ok := tr.Outcomes[key]; ok {
				t.Errorf("%s: outside-policy subject %s carries outcome %v", phase, key, got)
			}
		}
		wantReg := verify.Registration{Package: "example.com/wit/covered", Test: "TestOne", Requirement: "REQ-wit-probe"}
		found := false
		for _, reg := range tr.Registrations {
			if reg == wantReg {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: registration missing: %+v", phase, tr.Registrations)
		}
		requireNoSentinels(t, tmp)
	}

	cold, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	requirePartition("cold", cold, 0, 2)
	if cold.Uncached != 0 {
		t.Errorf("cold: uncached=%d, want 0: both executed subjects publish", cold.Uncached)
	}
	cache := witnesscache.Load(tmp)
	if len(cache) != 2 {
		t.Fatalf("cold run published %d records, want 2: %+v", len(cache), cache)
	}
	for _, outside := range []struct{ pkg, test string }{
		{"example.com/wit/uncovered", "TestNone"},
		{"example.com/wit/nonrace", "TestPlain"},
		{"example.com/wit/twice", "TestTwice"},
	} {
		if cacheRecord(t, cache, outside.pkg, outside.test) != nil {
			t.Errorf("outside-policy subject %s.%s published a record", outside.pkg, outside.test)
		}
	}

	warm, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	requirePartition("warm", warm, 2, 0)

	// Stale exactly one subject: the edit reaches TestOne's closure and
	// not TestTwo's, so only TestOne re-executes while its sibling serves.
	onePath := filepath.Join(tmp, "covered", "one.go")
	src, err := os.ReadFile(onePath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(src), "one-v1", "one-v2", 1)
	if edited == string(src) {
		t.Fatal("fixture edit failed")
	}
	if err := os.WriteFile(onePath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	staleRun, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	requirePartition("stale sibling", staleRun, 1, 1)
}

// TestGoRunWitnessesIsolatesDeniedOutcomes pins that the selective
// executor's isolation pass flows through the runner: a completed pass
// inside a red process and a test shadowed by a sibling's abort both gain
// real solo outcomes attributed to their own producing processes, the
// denying failures stand, and only subjects a healthy process granted
// publish records.
func TestGoRunWitnessesIsolatesDeniedOutcomes(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-policy-attribution")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/iso\n\ngo 1.26\n",
		"red/red_test.go": `package red

import "testing"

func TestGreen(t *testing.T) {}

func TestRed(t *testing.T) {
	t.Fatal("deliberately red")
}
`,
		"boom/boom_test.go": `package boom

import "testing"

func TestBoom(t *testing.T) {
	panic("fixture panic")
}

func TestShadowed(t *testing.T) {}
`,
		"state/state_test.go": `package state

import (
	"os"
	"testing"
)

var ready bool

func TestSetup(t *testing.T) { ready = true }

func TestNeedsSetup(t *testing.T) {
	if !ready {
		os.Exit(3)
	}
}

func TestStateRed(t *testing.T) { t.Fail() }
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("freshness path degraded: %s", tr.Degraded)
	}
	want := map[string]verify.TestOutcome{
		"example.com/iso/red.TestGreen":      verify.TestPassed,
		"example.com/iso/red.TestRed":        verify.TestFailed,
		"example.com/iso/boom.TestShadowed":  verify.TestPassed,
		"example.com/iso/boom.TestBoom":      verify.TestFailed,
		"example.com/iso/state.TestSetup":    verify.TestPassed,
		"example.com/iso/state.TestStateRed": verify.TestFailed,
	}
	for key, wantOutcome := range want {
		if got := tr.Outcomes[key]; got != wantOutcome {
			t.Errorf("%s = %v, want %v", key, got, wantOutcome)
		}
	}
	// A completed pass inside the red process whose solo re-run dies
	// before a terminal event grants nothing: a red process yields no
	// green evidence, and the isolated outcome is the only path back.
	if got, ok := tr.Outcomes["example.com/iso/state.TestNeedsSetup"]; ok {
		t.Errorf("state.TestNeedsSetup = %v, want no outcome: its only pass rode a red process", got)
	}
	// The solo-granted passes publish from their own healthy processes;
	// the failing tests — and the state package, whose closure reaches
	// os.Exit unverifiably — stay uncacheable.
	if tr.Fresh != 0 || tr.Ran != 7 || tr.Uncached != 5 {
		t.Errorf("fresh=%d ran=%d uncached=%d, want 0/7/5", tr.Fresh, tr.Ran, tr.Uncached)
	}
	cache := witnesscache.Load(tmp)
	if cacheRecord(t, cache, "example.com/iso/red", "TestGreen") == nil {
		t.Error("isolated green-in-red pass did not publish from its solo process")
	}
	if cacheRecord(t, cache, "example.com/iso/boom", "TestShadowed") == nil {
		t.Error("unshadowed pass did not publish from its solo process")
	}
	if cacheRecord(t, cache, "example.com/iso/red", "TestRed") != nil {
		t.Error("red test published a record")
	}
	if cacheRecord(t, cache, "example.com/iso/boom", "TestBoom") != nil {
		t.Error("aborting test published a record")
	}
}

// TestGoRunWitnessesServedDriftRetriesOnce pins post-run served-record
// revalidation: a record that checked valid before execution and whose
// observed input another package's execution then mutated mid-run has its
// served outcome discarded and its subject re-executed once within the
// same run, so the run's evidence never reports a serve the current tree
// disproves.
func TestGoRunWitnessesServedDriftRetriesOnce(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":          "module example.com/driftserve\n\ngo 1.26\n",
		"reader/data.txt": "v1\n",
		"reader/reader_test.go": `package reader

import (
	"os"
	"testing"
)

func TestReads(t *testing.T) {
	_, _ = os.ReadFile("data.txt")
}
`,
		"writer/trigger.txt": "no\n",
		"writer/writer_test.go": `package writer

import (
	"os"
	"strings"
	"testing"
)

//gofresh:pure
func TestWritesOnce(t *testing.T) {
	raw, err := os.ReadFile("trigger.txt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != "yes" {
		return
	}
	if err := os.WriteFile("../reader/data.txt", []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	cold, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if cold.Degraded != "" {
		t.Fatalf("cold: freshness path degraded: %s", cold.Degraded)
	}
	if cold.Fresh != 0 || cold.Ran != 2 {
		t.Fatalf("cold: fresh=%d ran=%d, want 0/2", cold.Fresh, cold.Ran)
	}
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/driftserve/reader", "TestReads") == nil {
		t.Fatal("cold run published no record for the reader; the drift would prove nothing")
	}

	// Arm the writer: its own runtime input moves, so it re-executes and
	// mutates the reader's observed fixture mid-run — after the reader's
	// record checked valid, before the post-run revalidation.
	if err := os.WriteFile(filepath.Join(tmp, "writer", "trigger.txt"), []byte("yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	drift, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if drift.Degraded != "" {
		t.Fatalf("drift: freshness path degraded: %s", drift.Degraded)
	}
	// The reader's serve is discarded and the subject re-executes once:
	// nothing serves, both packages carry executed outcomes.
	if drift.Fresh != 0 || drift.Ran != 2 {
		t.Errorf("drift: fresh=%d ran=%d, want 0/2: the drifted serve must be discarded and re-executed", drift.Fresh, drift.Ran)
	}
	for _, key := range []string{
		"example.com/driftserve/reader.TestReads",
		"example.com/driftserve/writer.TestWritesOnce",
	} {
		if got := drift.Outcomes[key]; got != verify.TestPassed {
			t.Errorf("drift: %s = %v, want PASSED", key, got)
		}
	}
	// The retried reader republishes against the settled tree, and the
	// purity-asserted writer republishes under its author's opt-in.
	if drift.Uncached != 0 {
		t.Errorf("drift: uncached=%d, want 0", drift.Uncached)
	}
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/driftserve/reader", "TestReads") == nil {
		t.Error("retried reader did not republish against the settled tree")
	}

	// The settled tree serves the retried record beside the writer's.
	settled, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Degraded != "" {
		t.Fatalf("settled: freshness path degraded: %s", settled.Degraded)
	}
	if settled.Fresh != 2 || settled.Ran != 0 {
		t.Errorf("settled: fresh=%d ran=%d, want 2/0", settled.Fresh, settled.Ran)
	}
	if got := settled.Outcomes["example.com/driftserve/reader.TestReads"]; got != verify.TestPassed {
		t.Errorf("settled: served reader outcome = %v, want PASSED", got)
	}
}

// TestGoRunWitnessesDegradesToFullExecution pins the degrade rule: a
// fault on the freshness path — here an analysis view that cannot be
// built over a package whose test sources fail to load — serves nothing
// and executes every covered subject, with the fault named on the result
// and the existing cache left alone. The full witnessing run is the
// selective runner with an empty served set.
func TestGoRunWitnessesDegradesToFullExecution(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-freshness-degrade")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/degrade\n\ngo 1.26\n",
		"fine/fine_test.go": `package fine

import "testing"

//gofresh:pure
func TestOK(t *testing.T) {}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	cold, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if cold.Degraded != "" || cold.Ran != 1 {
		t.Fatalf("cold: degraded=%q ran=%d, want a clean single-subject run", cold.Degraded, cold.Ran)
	}
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/degrade/fine", "TestOK") == nil {
		t.Fatal("cold run published no record; the degrade would prove nothing about serving")
	}

	// A new package whose second test file fails to parse: discovery still
	// enumerates the parseable test, but the analysis view over the group
	// cannot be built — a freshness-path fault.
	for path, content := range map[string]string{
		"broken/ok_test.go": `package broken

import "testing"

func TestFine(t *testing.T) {}
`,
		"broken/broken_test.go": "package broken\n\nfunc {\n",
	} {
		full := filepath.Join(tmp, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	degraded, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if degraded.Degraded == "" {
		t.Fatal("freshness-path fault did not name a degraded reason")
	}
	if degraded.Fresh != 0 {
		t.Errorf("degraded run served %d records; a degraded run serves nothing", degraded.Fresh)
	}
	// The cached, still-valid subject re-executes rather than serving.
	if got := degraded.Outcomes["example.com/degrade/fine.TestOK"]; got != verify.TestPassed {
		t.Errorf("covered subject did not re-execute under degrade: %v", got)
	}
	// The unloadable package's execution is attempted and build-fails: no
	// outcome, and the build failure rides the result keyed by package —
	// the denied subject's visibility story.
	if got, ok := degraded.Outcomes["example.com/degrade/broken.TestFine"]; ok {
		t.Errorf("unbuildable package produced outcome %v", got)
	}
	if degraded.PackageFailures["example.com/degrade/broken"] == "" {
		t.Errorf("unbuildable package carries no package-keyed diagnostic: %+v", degraded.PackageFailures)
	}
	if degraded.Uncached != degraded.Ran {
		t.Errorf("uncached=%d ran=%d, want every executed subject counted uncacheable", degraded.Uncached, degraded.Ran)
	}
	// The degraded path leaves the cache alone: the prior record survives.
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/degrade/fine", "TestOK") == nil {
		t.Error("degraded run dropped the existing cache")
	}
}

// TestGoRunWitnessesSoloSelectiveProcessPublishesProof pins the
// per-process observation-proof rule on the selective path: proof
// eligibility keys on the selective process running exactly one selected
// top-level runnable, not on the package's whole test population. The
// first run reds the package process — the flaky test fails once, so its
// record is refused while its denied sibling publishes from its solo
// isolation process — and the second run's one-stale-test selective
// process in the same many-test package publishes with an attached
// observation-completeness proof while the sibling serves.
func TestGoRunWitnessesSoloSelectiveProcessPublishesProof(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":       "module example.com/proofy\n\ngo 1.26\n",
		"pkg/flag.txt": "fail\n",
		"pkg/pkg_test.go": `package pkg

import (
	"os"
	"testing"
)

func TestFlakyReads(t *testing.T) {
	raw, _ := os.ReadFile("flag.txt")
	if string(raw) == "fail\n" {
		t.Fail()
	}
}

//gofresh:pure
func TestStable(t *testing.T) {}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	red, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if red.Degraded != "" {
		t.Fatalf("red: freshness path degraded: %s", red.Degraded)
	}
	if got := red.Outcomes["example.com/proofy/pkg.TestFlakyReads"]; got != verify.TestFailed {
		t.Fatalf("red: flaky outcome = %v, want FAILED", got)
	}
	if got := red.Outcomes["example.com/proofy/pkg.TestStable"]; got != verify.TestPassed {
		t.Fatalf("red: denied sibling = %v, want PASSED from its solo process", got)
	}
	cache := witnesscache.Load(tmp)
	if cacheRecord(t, cache, "example.com/proofy/pkg", "TestFlakyReads") != nil {
		t.Fatal("red run published a record for the failing test")
	}
	if cacheRecord(t, cache, "example.com/proofy/pkg", "TestStable") == nil {
		t.Fatal("denied sibling did not publish from its solo isolation process")
	}

	// Settle the flaky test's observed input: only the recordless test
	// re-executes, in a selective process of its own.
	if err := os.WriteFile(filepath.Join(tmp, "pkg", "flag.txt"), []byte("pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	solo, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if solo.Degraded != "" {
		t.Fatalf("solo: freshness path degraded: %s", solo.Degraded)
	}
	if solo.Fresh != 1 || solo.Ran != 1 {
		t.Fatalf("solo: fresh=%d ran=%d, want the stable sibling served and the recordless test re-executed", solo.Fresh, solo.Ran)
	}
	if got := solo.Outcomes["example.com/proofy/pkg.TestFlakyReads"]; got != verify.TestPassed {
		t.Errorf("solo: flaky outcome = %v, want PASSED", got)
	}
	rec := cacheRecord(t, witnesscache.Load(tmp), "example.com/proofy/pkg", "TestFlakyReads")
	if rec == nil {
		t.Fatal("solo selective process published no record")
	}
	if rec.Fingerprint.ObservationProof == nil || rec.Fingerprint.ObservationAssertion == "" {
		t.Errorf("solo selective process record carries no observation proof: %+v", rec.Fingerprint)
	} else if rec.Fingerprint.ObservationProof.Package != rec.Package ||
		rec.Fingerprint.ObservationProof.Symbol != rec.Test ||
		!rec.Fingerprint.ObservationProof.Observable {
		t.Errorf("observation proof does not attest the record's own subject: %+v", rec.Fingerprint.ObservationProof)
	}
}

// TestGoRunWitnessesDeniesEnvelopeCutoffPasses pins the per-process
// evidence gate where the isolation pass cannot repair it: a completed
// pass inside a process the envelope cut off is denied — a TIMEOUT
// process yields no green evidence and expiry denies the solo re-run
// before it spawns — so the pass neither witnesses nor publishes.
func TestGoRunWitnessesDeniesEnvelopeCutoffPasses(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-policy-attribution")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/cutoff\n\ngo 1.26\n",
		"cut/cut_test.go": `package cut

import (
	"testing"
	"time"
)

func TestQuick(t *testing.T) {}

func TestSleeps(t *testing.T) {
	time.Sleep(10 * time.Minute)
}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	inv := goInvocation("all", cfg)
	// The reviewed envelope admits the build and the quick test, never the
	// sleeper: the process is cut off with the quick pass already parsed.
	inv.SetTimeout(durationpb.New(20 * time.Second))
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{inv})
	writePolicyRecord(t, tmp, p)

	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("freshness path degraded: %s", tr.Degraded)
	}
	for _, key := range []string{
		"example.com/cutoff/cut.TestQuick",
		"example.com/cutoff/cut.TestSleeps",
	} {
		if got, ok := tr.Outcomes[key]; ok {
			t.Errorf("%s = %v, want no outcome: a cut-off process grants nothing", key, got)
		}
	}
	// A denied subject appears in no bucket: nothing served, nothing
	// reached a terminal event, nothing is outside the policy.
	if tr.Fresh != 0 || tr.Ran != 0 || tr.OutsidePolicy != 0 {
		t.Errorf("fresh=%d ran=%d outside=%d, want 0/0/0: denied subjects belong to no bucket", tr.Fresh, tr.Ran, tr.OutsidePolicy)
	}
	// The denied subjects' visibility is their package-keyed diagnostic:
	// the cutoff must be traceable from the result.
	if tr.PackageFailures["example.com/cutoff/cut"] == "" {
		t.Errorf("cut-off package carries no package-keyed diagnostic: %+v", tr.PackageFailures)
	}
	if len(witnesscache.Load(tmp)) != 0 {
		t.Errorf("a cut-off process published records: %+v", witnesscache.Load(tmp))
	}
}

// TestGoRunWitnessesDegradesOnUniverseFault pins that a fault while
// discovering the tree's obligation universe degrades exactly as any
// other freshness-path fault: the run serves nothing and executes every
// subject the policy covers, with the universe fault named on the result
// — never an error, because selection needs only the policy's own
// discovery, and work the policy leaves outside witnessing stays outside
// degraded or not.
func TestGoRunWitnessesDegradesOnUniverseFault(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-freshness-degrade")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.work": "go 1.26\n\nuse .\n",
		"go.mod":  "module example.com/uni\n\ngo 1.26\n",
		"covered/covered_test.go": `package covered

import "testing"

//gofresh:pure
func TestOK(t *testing.T) {}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./covered"})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	cold, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if cold.Degraded != "" || cold.Ran != 1 {
		t.Fatalf("cold: degraded=%q ran=%d, want a clean single-subject run", cold.Degraded, cold.Ran)
	}
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/uni/covered", "TestOK") == nil {
		t.Fatal("cold run published no record; the degrade would prove nothing about serving")
	}

	// A workspace member whose baseline discovery selects no packages
	// faults the universe walk while the policy's own selection stays
	// whole: the policy invocation never touches the new member.
	if err := os.WriteFile(filepath.Join(tmp, "go.work"), []byte("go 1.26\n\nuse (\n\t.\n\t./empty\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "empty", "go.mod"), []byte("module example.com/uni/empty\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	degraded, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatalf("universe fault surfaced as an error instead of degrading: %v", err)
	}
	if degraded.Degraded == "" {
		t.Fatal("universe fault did not name a degraded reason")
	}
	if degraded.Fresh != 0 {
		t.Errorf("degraded run served %d records; a degraded run serves nothing", degraded.Fresh)
	}
	if got := degraded.Outcomes["example.com/uni/covered.TestOK"]; got != verify.TestPassed {
		t.Errorf("covered subject did not re-execute under degrade: %v", got)
	}
	if degraded.Uncached != degraded.Ran {
		t.Errorf("uncached=%d ran=%d, want every executed subject counted uncacheable", degraded.Uncached, degraded.Ran)
	}
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/uni/covered", "TestOK") == nil {
		t.Error("degraded run dropped the existing cache")
	}
}

// TestGoRunWitnessesCancellationDiscardsRun pins the discard contract on
// the selective witness surface (REQ-policy-cancellation): a caller
// cancellation mid-execution discards the partial run whole — the
// selective executor returns no partial SelectionResult, the runner
// returns no partial TestRun, and the witness cache is left byte-for-byte
// untouched.
func TestGoRunWitnessesCancellationDiscardsRun(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-cancellation")
	if testing.Short() {
		t.Skip("executes race-instrumented selective runs over temporary modules")
	}
	neutralAmbient(t)

	sleepyModule := func(module string) (string, string) {
		tmp := writeModule(t, map[string]string{
			"go.mod": "module " + module + "\n\ngo 1.26\n",
			"sleepy/sleepy_test.go": `package sleepy

import (
	"os"
	"testing"
	"time"
)

func TestSleeps(t *testing.T) {
	_ = os.WriteFile("started.sentinel", nil, 0o644)
	time.Sleep(10 * time.Minute)
}
`,
		})
		return tmp, filepath.Join(tmp, "sleepy", "started.sentinel")
	}
	// cancelOnSentinel fires the cancellation only once the test binary
	// is demonstrably executing, so the cancel always lands mid-run,
	// never before the spawn.
	cancelOnSentinel := func(ctx context.Context, cancel context.CancelFunc, sentinel string) {
		go func() {
			deadline := time.Now().Add(3 * time.Minute)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(sentinel); err == nil {
					cancel()
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(25 * time.Millisecond):
				}
			}
			cancel()
		}()
	}

	// The selective executor discards the partial selection result.
	selDir, selSentinel := sleepyModule("example.com/cancelsel")
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./sleepy"})
	cfg.SetRace(true)
	n, err := NormalizeInvocation(context.Background(), selDir, goInvocation("solo", cfg))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelOnSentinel(ctx, cancel, selSentinel)
	res, err := ExecuteSelection(ctx, n, TestSelection{"example.com/cancelsel/sleepy": {"TestSleeps"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteSelection err = %v, want context.Canceled", err)
	}
	if res != nil {
		t.Fatalf("partial selection result escaped a cancelled execution: %+v", res)
	}

	// The runner discards identically and leaves the cache untouched.
	runDir, runSentinel := sleepyModule("example.com/cancelrun")
	writeRacePolicy(t, runDir)
	cachePath := filepath.Join(runDir, filepath.FromSlash(witnesscache.Path))
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := []byte(`{"version":3,"records":[]}`)
	if err := os.WriteFile(cachePath, seed, 0o644); err != nil {
		t.Fatal(err)
	}
	rctx, rcancel := context.WithCancel(context.Background())
	defer rcancel()
	cancelOnSentinel(rctx, rcancel, runSentinel)
	tr, err := RunWitnesses(rctx, runDir)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunWitnesses err = %v, want context.Canceled", err)
	}
	if tr != nil {
		t.Fatalf("partial test run escaped a cancelled witnessing run: %+v", tr)
	}
	after, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(seed, after) {
		t.Fatalf("cancelled run touched the witness cache:\nseed:  %s\nafter: %s", seed, after)
	}
}
