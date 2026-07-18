package golang

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

// writeModule lays out a temporary Go module from path→content pairs.
func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	tmp := t.TempDir()
	for path, content := range files {
		full := filepath.Join(tmp, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tmp
}

func cacheRecord(t *testing.T, records []witnesscache.Record, pkg, test string) *witnesscache.Record {
	t.Helper()
	for i := range records {
		if records[i].Package == pkg && records[i].Test == test {
			return &records[i]
		}
	}
	return nil
}

// TestGoDeriveUnifiedExecutionEvidence pins one policy execution end to
// end through the derivation seam: suite health and witness evidence come
// from the same report; a green test beside a red TestMain grants
// nothing and its previously cached green record is dropped rather than
// serving health or evidence; a passing example beside a failing one
// grants nothing; a killed package's shadowed sibling has no outcome; a
// healthy non-race invocation contributes suite health but no witness;
// and only healthy race-produced tests publish freshness records, each
// carrying its own producing process's runtime-input manifest and never a
// sibling process's.
func TestGoDeriveUnifiedExecutionEvidence(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness", "REQ-go-witness", "REQ-go-race",
		"REQ-evidence-freshness-no-health", "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented policy over the execute fixture")
	}
	neutralAmbient(t)
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS("testdata/execute")); err != nil {
		t.Fatal(err)
	}
	// Seed loadable records: a green one beside the newly red TestMain and
	// a stale one contradicting the current tree (neither may grant
	// anything, both are superseded by publication), plus one for the test
	// its sibling's kill will shadow — that test produces no row, and a
	// record this execution never touched is retained, never silently
	// dropped.
	seedFP := witnesscache.Fingerprint{
		MaximalClosure: "00112233445566778899aabbccddeeff",
		Toolchain:      "go1.26",
		BuildConfig:    "00112233445566778899aabbccddeeff",
		RuntimeInputs:  "eyJ2IjoxfQ",
		RuntimeDigest:  "00112233445566778899aabbccddeeff",
		ResultKind:     gofresh.CodeResult,
	}
	if err := witnesscache.Save(tmp, []witnesscache.Record{
		{Package: "example.com/exec/redmain", Test: "TestGreen", Fingerprint: seedFP,
			Outcomes: map[string]string{"example.com/exec/redmain.TestGreen": "passed"}},
		{Package: "example.com/exec/ok", Test: "TestDouble", Fingerprint: seedFP,
			Outcomes: map[string]string{"example.com/exec/ok.TestDouble": "failed"}},
		{Package: "example.com/exec/killmid", Test: "TestShadowedByKill", Fingerprint: seedFP,
			Outcomes: map[string]string{"example.com/exec/killmid.TestShadowedByKill": "passed"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(witnesscache.Load(tmp)) != 3 {
		t.Fatal("seeded cache records are not loadable; the seeds would prove nothing")
	}

	raceCfg := &stipulatorv1.GoInvocationConfig{}
	raceCfg.SetPackages([]string{"./ok", "./reads", "./redmain", "./killmid", "./examples"})
	raceCfg.SetRace(true)
	plainCfg := &stipulatorv1.GoInvocationConfig{}
	plainCfg.SetPackages([]string{"./sleepy"})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("a-race", raceCfg),
		goInvocation("z-plain", plainCfg),
	})

	report, tr, err := ExecutePolicyWitnessed(context.Background(), tmp, p)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("publication degraded: %s", tr.Degraded)
	}
	if SuiteHealthy(report) {
		t.Error("suite with red packages read healthy")
	}
	byName := map[string]*stipulatorv1.InvocationHealth{}
	for _, h := range report.GetInvocations() {
		byName[h.GetInvocation()] = h
	}
	if got := byName["z-plain"].GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Errorf("non-race invocation = %v, want HEALTHY: it contributes suite health like any other", got)
	}

	wantOutcome := map[string]verify.TestOutcome{
		"example.com/exec/ok.TestDouble":              verify.TestPassed,
		"example.com/exec/ok.TestDouble/zero":         verify.TestPassed,
		"example.com/exec/ok.TestSkipped":             verify.TestSkipped,
		"example.com/exec/reads.TestReadsFixture":     verify.TestPassed,
		"example.com/exec/examples.Example_fail":      verify.TestFailed,
		"example.com/exec/redmain.TestGreen":          verify.TestNotRun,
		"example.com/exec/examples.Example_pass":      verify.TestNotRun,
		"example.com/exec/killmid.TestKilledMidRun":   verify.TestNotRun,
		"example.com/exec/killmid.TestShadowedByKill": verify.TestNotRun,
		"example.com/exec/sleepy.TestSleeps":          verify.TestNotRun,
	}
	for key, want := range wantOutcome {
		got, ok := tr.Outcomes[key]
		if want == verify.TestNotRun {
			if ok {
				t.Errorf("%s = %v, want no outcome", key, got)
			}
			continue
		}
		if got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	// Executed top-level tests: TestDouble, TestSkipped, TestReadsFixture,
	// TestGreen, TestSleeps. Published: the three healthy race ones; the
	// red-TestMain green and the non-race pass stay visibly uncacheable.
	if tr.Ran != 5 || tr.Fresh != 0 || tr.Uncached != 2 {
		t.Errorf("ran=%d fresh=%d uncached=%d, want 5/0/2", tr.Ran, tr.Fresh, tr.Uncached)
	}

	cache := witnesscache.Load(tmp)
	if len(cache) != 4 {
		t.Fatalf("cache carries %d records, want 4 (3 published + 1 retained): %+v", len(cache), cache)
	}
	for _, dropped := range []struct{ pkg, test string }{
		{"example.com/exec/redmain", "TestGreen"},
		{"example.com/exec/killmid", "TestKilledMidRun"},
		{"example.com/exec/sleepy", "TestSleeps"},
	} {
		if cacheRecord(t, cache, dropped.pkg, dropped.test) != nil {
			t.Errorf("record for %s.%s published from an unhealthy, aborted, or non-race source", dropped.pkg, dropped.test)
		}
	}
	// The shadowed test produced no row, so its prior record — a
	// selective run's legitimate publication — is retained untouched:
	// dropping it would shrink the cache for work this run never re-did.
	shadowed := cacheRecord(t, cache, "example.com/exec/killmid", "TestShadowedByKill")
	if shadowed == nil {
		t.Error("prior record for the shadowed test was silently dropped")
	} else if shadowed.Fingerprint.MaximalClosure != seedFP.MaximalClosure {
		t.Error("shadowed test's record was republished rather than retained")
	}
	double := cacheRecord(t, cache, "example.com/exec/ok", "TestDouble")
	if double == nil {
		t.Fatal("no record published for the healthy race-produced test")
	}
	if double.Outcomes["example.com/exec/ok.TestDouble"] != "passed" ||
		double.Outcomes["example.com/exec/ok.TestDouble/zero"] != "passed" {
		t.Errorf("stale seeded record was not replaced by the executed outcomes: %v", double.Outcomes)
	}
	reads := cacheRecord(t, cache, "example.com/exec/reads", "TestReadsFixture")
	if reads == nil {
		t.Fatal("no record published for the fixture-reading test")
	}
	readsPaths, err := runtimeinput.ModuleRelPaths(reads.Fingerprint.RuntimeInputs)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(readsPaths, "reads/testdata/fixture.txt") {
		t.Errorf("reads record does not carry its own process's observation: %v", readsPaths)
	}
	doublePaths, err := runtimeinput.ModuleRelPaths(double.Fingerprint.RuntimeInputs)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(doublePaths, "reads/testdata/fixture.txt") {
		t.Errorf("sibling process's observation leaked into ok's record: %v", doublePaths)
	}
}

// TestGoDerivePublishesObservationProofForSoloProcess pins the
// observation-proof selection rule: a process running exactly one
// selected top-level test may publish with an attached
// observation-completeness proof, while a multi-test process publishes
// with the plain per-package manifest — sibling tests could contribute
// unrecorded process state, so proof selection is never inferred for
// them. Runtime registrations ride the published record. A package two
// invocations select can never publish, and its guaranteed ineligibility
// must not strip the proof from the group's publishable candidates.
func TestGoDerivePublishesObservationProofForSoloProcess(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented policy over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":        "module example.com/pub\n\ngo 1.26\n",
		"solo/data.txt": "v1\n",
		"solo/solo_test.go": `package solo

import (
	"os"
	"testing"
)

func TestReadsObserved(t *testing.T) {
	_, _ = os.ReadFile("data.txt")
}
`,
		"multi/multi_test.go": `package multi

import "testing"

//gofresh:pure
func TestOne(t *testing.T) {
	t.Log("stipulator:covers REQ-pub-probe")
}

//gofresh:pure
func TestTwo(t *testing.T) {
	t.Run("sub", func(t *testing.T) {})
}
`,
		"shared/data.txt": "v1\n",
		"shared/shared_test.go": `package shared

import (
	"os"
	"testing"
)

func TestSharedReads(t *testing.T) {
	_, _ = os.ReadFile("data.txt")
}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	// A second race invocation in another capture group (tag-widened)
	// double-selects the shared package: it can never publish, and its
	// ineligibility must leave the first group's proofs standing.
	dupCfg := &stipulatorv1.GoInvocationConfig{}
	dupCfg.SetPackages([]string{"./shared"})
	dupCfg.SetRace(true)
	dupCfg.SetTags([]string{"dup"})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg), goInvocation("dup", dupCfg)})

	report, tr, err := ExecutePolicyWitnessed(context.Background(), tmp, p)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("publication degraded: %s", tr.Degraded)
	}
	if !SuiteHealthy(report) {
		t.Fatalf("fixture suite unexpectedly unhealthy: %v", report.GetInvocations())
	}
	// The double-selected package executes and counts uncacheable; its
	// record never publishes.
	if tr.Ran != 4 || tr.Uncached != 1 {
		t.Errorf("ran=%d uncached=%d, want 4/1", tr.Ran, tr.Uncached)
	}
	cache := witnesscache.Load(tmp)
	if len(cache) != 3 {
		t.Fatalf("published %d records, want 3: %+v", len(cache), cache)
	}
	if cacheRecord(t, cache, "example.com/pub/shared", "TestSharedReads") != nil {
		t.Error("record published for a package two invocations select")
	}
	solo := cacheRecord(t, cache, "example.com/pub/solo", "TestReadsObserved")
	if solo == nil {
		t.Fatal("no record for the solo-process test")
	}
	if solo.Fingerprint.ObservationProof == nil || solo.Fingerprint.ObservationAssertion == "" {
		t.Errorf("solo-process record carries no observation proof: %+v", solo.Fingerprint)
	} else if solo.Fingerprint.ObservationProof.Package != solo.Package ||
		solo.Fingerprint.ObservationProof.Symbol != solo.Test ||
		!solo.Fingerprint.ObservationProof.Observable {
		t.Errorf("observation proof does not attest the record's own subject: %+v", solo.Fingerprint.ObservationProof)
	}
	for _, name := range []string{"TestOne", "TestTwo"} {
		rec := cacheRecord(t, cache, "example.com/pub/multi", name)
		if rec == nil {
			t.Fatalf("no record for multi-process test %s", name)
		}
		if rec.Fingerprint.ObservationProof != nil || rec.Fingerprint.ObservationAssertion != "" {
			t.Errorf("%s gained an observation proof from a process it shared with a sibling: %+v", name, rec.Fingerprint)
		}
		if rec.Fingerprint.RuntimeInputs == "" || rec.Fingerprint.RuntimeDigest == "" {
			t.Errorf("%s record carries no runtime-input manifest: %+v", name, rec.Fingerprint)
		}
	}
	two := cacheRecord(t, cache, "example.com/pub/multi", "TestTwo")
	if two.Outcomes["example.com/pub/multi.TestTwo/sub"] != "passed" {
		t.Errorf("subtest outcome missing from the record: %v", two.Outcomes)
	}
	one := cacheRecord(t, cache, "example.com/pub/multi", "TestOne")
	wantReg := verify.Registration{Package: "example.com/pub/multi", Test: "TestOne", Requirement: "REQ-pub-probe"}
	if !slices.Contains(one.Regs, wantReg) {
		t.Errorf("runtime registration missing from the record: %+v", one.Regs)
	}
}

// TestGoDeriveSourceDriftDegradesPublication pins source producer
// validation: a test that edits its own package source while the policy
// runs leaves the analysis view describing a tree the execution did not
// run against, so nothing publishes — while the executed outcome itself
// still witnesses, because evidence follows the execution, and the
// degradation is named rather than silent.
func TestGoDeriveSourceDriftDegradesPublication(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-evidence-freshness-degrade")
	if testing.Short() {
		t.Skip("executes a race-instrumented policy over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":    "module example.com/mutate\n\ngo 1.26\n",
		"mutate.go": "package mutate\n",
		"mutate_test.go": `package mutate

import (
	"os"
	"testing"
)

func TestMutatesSourceOnce(t *testing.T) {
	if _, err := os.Stat("mutated.once"); !os.IsNotExist(err) {
		return
	}
	source, err := os.ReadFile("mutate.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("mutate.go", append(source, []byte("\n// changed during run\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("mutated.once", nil, 0o644); err != nil {
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

	report, tr, err := ExecutePolicyWitnessed(context.Background(), tmp, p)
	if err != nil {
		t.Fatal(err)
	}
	if !SuiteHealthy(report) {
		t.Fatalf("fixture suite unexpectedly unhealthy: %v", report.GetInvocations())
	}
	if got := tr.Outcomes["example.com/mutate.TestMutatesSourceOnce"]; got != verify.TestPassed {
		t.Errorf("executed outcome = %v, want PASSED: evidence follows the execution", got)
	}
	if tr.Degraded == "" {
		t.Error("mid-run source drift published silently; the degradation must be named")
	}
	if tr.Uncached != tr.Ran {
		t.Errorf("uncached=%d ran=%d, want every executed test counted uncacheable", tr.Uncached, tr.Ran)
	}
	requireCacheAbsent(t, tmp)
}

// TestGoDeriveRuntimeDriftAndUnverifiableSkipRecords pins runtime
// producer validation per record: a package whose observed runtime input
// moved after its process ingested it (the purity-asserted reader, whose
// fixture a later invocation rewrites), and packages whose observations
// are unverifiable (a process-local environment read; a parent-traversal
// write), all execute and witness normally but publish nothing — their
// records are dropped and counted uncacheable so the next run re-derives
// them — while an unaffected package in the same execution still
// publishes.
func TestGoDeriveRuntimeDriftAndUnverifiableSkipRecords(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented policy over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":          "module example.com/drift\n\ngo 1.26\n",
		"reader/data.txt": "before",
		"reader/reader_test.go": `package reader

import (
	"os"
	"testing"
)

//gofresh:pure
func TestReads(t *testing.T) {
	if _, err := os.ReadFile("data.txt"); err != nil {
		t.Fatal(err)
	}
}

//gofresh:pure
func TestReaderNoop(t *testing.T) {}
`,
		"writer/writer_test.go": `package writer

import (
	"os"
	"testing"
)

func TestWritesOnce(t *testing.T) {
	if _, err := os.Stat("written.once"); !os.IsNotExist(err) {
		return
	}
	if err := os.WriteFile("../reader/data.txt", []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("written.once", nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriterNoop(t *testing.T) {}
`,
		"impure/impure_test.go": `package impure

import (
	"os"
	"testing"
)

func TestReadsProcessLocalEnv(t *testing.T) {
	_ = os.Getenv("PWD")
}

func TestImpureNoop(t *testing.T) {}
`,
		"clean/clean_test.go": `package clean

import "testing"

//gofresh:pure
func TestClean(t *testing.T) {}

//gofresh:pure
func TestCleanNoop(t *testing.T) {}
`,
	})
	first := &stipulatorv1.GoInvocationConfig{}
	first.SetPackages([]string{"./clean", "./impure", "./reader"})
	first.SetRace(true)
	second := &stipulatorv1.GoInvocationConfig{}
	second.SetPackages([]string{"./writer"})
	second.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	// Invocations execute sequentially in record order: the reader's
	// process completes and ingests its observation before the writer
	// mutates the observed file, so the drift deterministically lands
	// between ingestion and publication.
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("a-first", first),
		goInvocation("b-second", second),
	})

	report, tr, err := ExecutePolicyWitnessed(context.Background(), tmp, p)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("per-record drift degraded the whole publication path: %s", tr.Degraded)
	}
	if !SuiteHealthy(report) {
		t.Fatalf("fixture suite unexpectedly unhealthy: %v", report.GetInvocations())
	}
	for _, key := range []string{
		"example.com/drift/reader.TestReads",
		"example.com/drift/writer.TestWritesOnce",
		"example.com/drift/impure.TestReadsProcessLocalEnv",
	} {
		if got := tr.Outcomes[key]; got != verify.TestPassed {
			t.Errorf("%s = %v, want PASSED: dropping a record never touches evidence", key, got)
		}
	}
	cache := witnesscache.Load(tmp)
	for _, dropped := range []struct{ pkg, test string }{
		{"example.com/drift/reader", "TestReads"},
		{"example.com/drift/reader", "TestReaderNoop"},
		{"example.com/drift/impure", "TestReadsProcessLocalEnv"},
		{"example.com/drift/impure", "TestImpureNoop"},
		{"example.com/drift/writer", "TestWritesOnce"},
		{"example.com/drift/writer", "TestWriterNoop"},
	} {
		if cacheRecord(t, cache, dropped.pkg, dropped.test) != nil {
			t.Errorf("record for %s.%s published despite drifted or unverifiable runtime inputs", dropped.pkg, dropped.test)
		}
	}
	if cacheRecord(t, cache, "example.com/drift/clean", "TestClean") == nil ||
		cacheRecord(t, cache, "example.com/drift/clean", "TestCleanNoop") == nil {
		t.Errorf("unaffected package did not publish: %+v", cache)
	}
	if tr.Ran != 8 || tr.Uncached != 6 {
		t.Errorf("ran=%d uncached=%d, want 8/6", tr.Ran, tr.Uncached)
	}
}
