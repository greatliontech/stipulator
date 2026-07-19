package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// observedReaderModule writes a self-contained module whose one test reads a
// data file. The witness runner must prove that its completed runtime
// observation is complete instead of relying on a purity assertion.
func observedReaderModule(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/purefix\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "data.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testSource := `package purefix

import (
	"os"
	"testing"
)

func TestReadsObservedFixture(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = os.ReadFile("data.txt")
}
`
	if err := os.WriteFile(filepath.Join(tmp, "purefix_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRacePolicy(t, tmp)
	return tmp
}

// TestObservationProofPublishesAndServes pins the caller-selected proof end
// to end: a file-reading test without a purity directive publishes only after
// its completed observation is attached and validated, then serves through
// an explicitly observed check.
//
//gofresh:pure
func TestObservationProofPublishesAndServes(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if testing.Short() {
		t.Skip("executes a real race-instrumented witness suite")
	}
	tmp := observedReaderModule(t)

	first, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if first.Degraded != "" {
		t.Fatalf("first run degraded: %s", first.Degraded)
	}
	if first.Ran != 1 || first.Uncached != 0 {
		t.Fatalf("first run: ran=%d uncached=%d; the observation-proven record must publish", first.Ran, first.Uncached)
	}
	records := witnesscache.Load(tmp)
	if len(records) != 1 || records[0].Fingerprint.PurityAssertion != "" ||
		records[0].Fingerprint.ObservationAssertion != "caller assertion" ||
		records[0].Fingerprint.ObservationProof == nil ||
		records[0].Fingerprint.ObservationProof.Strategy != gofresh.ObservationRTA ||
		!records[0].Fingerprint.ObservationProof.Observable {
		t.Fatalf("published fingerprint lacks attributable positive observation proof: %+v", records)
	}

	second, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if second.Degraded != "" {
		t.Fatalf("second run degraded: %s", second.Degraded)
	}
	if second.Fresh != 1 || second.Ran != 0 {
		t.Fatalf("second run: fresh=%d ran=%d; the published record must serve", second.Fresh, second.Ran)
	}
	if second.Outcomes["example.com/purefix.TestReadsObservedFixture"] == 0 {
		t.Fatalf("served outcome missing: %v", second.Outcomes)
	}
}

// TestObservationProofNeverWaivesInputDigest pins that the proof suppresses
// only closure-level observation conservatism: a change to the observed data
// file still stales the record and re-runs the test.
//
//gofresh:pure
func TestObservationProofNeverWaivesInputDigest(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if testing.Short() {
		t.Skip("executes a real race-instrumented witness suite")
	}
	tmp := observedReaderModule(t)

	first, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	// The staling assertion below is meaningful only if the first run
	// actually published: stale-because-never-cached would pass vacuously.
	if first.Ran != 1 || first.Uncached != 0 {
		t.Fatalf("first run: ran=%d uncached=%d; the record must publish before the digest can stale it", first.Ran, first.Uncached)
	}
	if err := os.WriteFile(filepath.Join(tmp, "data.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if second.Degraded != "" {
		t.Fatalf("second run degraded: %s", second.Degraded)
	}
	if second.Ran != 1 || second.Fresh != 0 {
		t.Fatalf("after input change: ran=%d fresh=%d; the digest must stale the record", second.Ran, second.Fresh)
	}
}

//gofresh:pure
func TestObservationProofDoesNotPublishUnverifiableRuntimeState(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if testing.Short() {
		t.Skip("executes a real race-instrumented witness suite")
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/statfix\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "fixture"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	testSource := `package statfix

import (
	"os"
	"testing"
)

func TestStatsFixture(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := os.Stat("fixture"); err != nil {
		t.Fatal(err)
	}
}
`
	if err := os.WriteFile(filepath.Join(tmp, "statfix_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRacePolicy(t, tmp)

	result, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ran != 1 || result.Uncached != 1 {
		t.Fatalf("run: ran=%d uncached=%d; unverifiable runtime state must not publish", result.Ran, result.Uncached)
	}
	if records := witnesscache.Load(tmp); len(records) != 0 {
		t.Fatalf("unverifiable runtime state published: %+v", records)
	}
}

func TestValidatedObservationRequiresVerifiableRuntimeState(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	positive := gofresh.Fingerprint{ObservationProof: gofresh.ObservationProof{Observable: true}}
	if validatedObservation(positive, runtimeinput.State{Unverifiable: true}) {
		t.Fatal("positive proof validated an unverifiable runtime state")
	}
	if !validatedObservation(positive, runtimeinput.State{}) {
		t.Fatal("positive proof rejected a verifiable runtime state")
	}
	if validatedObservation(gofresh.Fingerprint{}, runtimeinput.State{}) {
		t.Fatal("missing proof validated a runtime state")
	}
}

// TestIncompatibleObservationEvidenceCannotServe pins the cache boundary: a
// canonical structural digest remains readable without source loading, but
// CheckObserved rejects evidence that is not bound to the current proof.
//
//gofresh:pure
func TestIncompatibleObservationEvidenceCannotServe(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if testing.Short() {
		t.Skip("executes a real race-instrumented witness suite")
	}
	tmp := observedReaderModule(t)
	first, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if first.Ran != 1 || first.Uncached != 0 {
		t.Fatalf("first run: ran=%d uncached=%d; record did not publish", first.Ran, first.Uncached)
	}
	records := witnesscache.Load(tmp)
	if len(records) != 1 || records[0].Fingerprint.ObservationProof == nil {
		t.Fatalf("published proof missing: %+v", records)
	}
	evidence := records[0].Fingerprint.ObservationProof.Evidence
	incompatible := strings.Repeat("0", 32)
	if incompatible == evidence {
		incompatible = strings.Repeat("1", 32)
	}
	store, err := witnesscache.StoreDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	variants, err := filepath.Glob(filepath.Join(store, "*.json"))
	if err != nil || len(variants) != 1 {
		t.Fatalf("variant files = %v (%v), want exactly one", variants, err)
	}
	if err := os.Remove(variants[0]); err != nil {
		t.Fatal(err)
	}
	rec := records[0]
	rec.Fingerprint.ObservationProof.Evidence = incompatible
	if err := witnesscache.Install(tmp, rec); err != nil {
		t.Fatal(err)
	}
	if got := witnesscache.Load(tmp); len(got) != 1 {
		t.Fatalf("canonical incompatible proof was not structurally readable: %+v", got)
	}

	second, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if second.Degraded != "" {
		t.Fatalf("incompatible proof degraded freshness: %s", second.Degraded)
	}
	if second.Fresh != 0 || second.Ran != 1 {
		t.Fatalf("incompatible proof served: fresh=%d ran=%d", second.Fresh, second.Ran)
	}
}

// TestObservationProofRequiresIsolatedTestProcess pins attribution at the
// process boundary: one sibling mutates process state and another consumes it,
// so neither outcome may receive a per-subject completeness proof.
//
//gofresh:pure
func TestObservationProofRequiresIsolatedTestProcess(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if testing.Short() {
		t.Skip("executes a real race-instrumented witness suite")
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/siblingfix\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "data.txt"), []byte("guarded input"), 0o644); err != nil {
		t.Fatal(err)
	}
	testSource := `package siblingfix

import (
	"os"
	"testing"
)

var siblingState string

func TestAChangesProcessState(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = os.ReadFile("data.txt")
	siblingState = "set-by-sibling"
}

func TestBDependsOnSiblingState(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = os.ReadFile("data.txt")
	if siblingState != "set-by-sibling" {
		return
	}
}
`
	if err := os.WriteFile(filepath.Join(tmp, "sibling_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRacePolicy(t, tmp)
	for run := 1; run <= 2; run++ {
		result, err := RunWitnesses(context.Background(), tmp)
		if err != nil {
			t.Fatal(err)
		}
		if result.Degraded != "" {
			t.Fatalf("run %d degraded: %s", run, result.Degraded)
		}
		if result.Ran != 2 || result.Fresh != 0 || result.Uncached != 2 {
			t.Fatalf("run %d: ran=%d fresh=%d uncached=%d; shared-process outcomes must rerun", run, result.Ran, result.Fresh, result.Uncached)
		}
	}
}
