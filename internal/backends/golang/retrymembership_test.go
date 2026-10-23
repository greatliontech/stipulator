package golang

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestRetryRunnableRemovalRevokesOnlyTheRemovedGrant(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a later retry wave that renames an earlier passing runnable")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-revalidation", "REQ-evidence-witness-freshness-interference")
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	b := "package b\nimport (\"os\"; \"testing\")\n//gofresh:pure\nfunc TestB(t *testing.T) { value, err := os.ReadFile(\"trigger\"); if err != nil { t.Fatal(err) }; if string(value) == \"yes\" { if err := os.WriteFile(\"../a/data\", []byte(\"moved\"), 0600); err != nil { t.Fatal(err) } } }\n"
	b += "//gofresh:pure\nfunc TestKeep(t *testing.T) {}\n"
	a := fmt.Sprintf("package a\nimport (\"os\"; \"testing\")\n//gofresh:pure\nfunc TestA(t *testing.T) { value, err := os.ReadFile(\"data\"); if err != nil { t.Fatal(err) }; if string(value) == \"moved\" { if err := os.WriteFile(\"../b/b_test.go\", []byte(%q), 0600); err != nil { t.Fatal(err) } } }\n", strings.Replace(b, "func TestB(", "func TestOther(", 1))
	c := "package c\nimport (\"os\"; \"testing\")\nconst armed = false\n//gofresh:pure\nfunc TestC(t *testing.T) { if armed { if err := os.WriteFile(\"../b/trigger\", []byte(\"yes\"), 0600); err != nil { t.Fatal(err) } } }\n"
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/removedretry\n\ngo 1.26\n", "a/data": "old", "b/trigger": "no", "a/a_test.go": a, "b/b_test.go": b, "c/c_test.go": c})
	retryLifecyclePolicy(t, dir) // PGO is off: removal is independent of metadata/profile drift.
	if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Uncached != 0 {
		t.Fatalf("cold: %+v %v", run, err)
	}
	if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Fresh != 4 {
		t.Fatalf("warm: %+v %v", run, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c/c_test.go"), []byte(strings.Replace(c, "armed = false", "armed = true", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	rawBPassed := false
	attempts := map[string]int{}
	prior := afterRetryWaveForTest
	afterRetryWaveForTest = func(merge *execMerge) {
		for _, row := range merge.rows {
			if row.GetTest() != topLevel(row.GetTest()) {
				continue
			}
			attempts[row.GetPackage()+"."+row.GetTest()]++
			if row.GetPackage() == "example.com/removedretry/b" && row.GetTest() == "TestB" {
				rawBPassed = row.GetOutcome() == passed && merge.disp[keyOfProducer(row.GetProducer())] == healthy
			}
		}
	}
	t.Cleanup(func() { afterRetryWaveForTest = prior })
	run, err := RunWitnesses(t.Context(), dir, noSeeding{})
	if err != nil || !rawBPassed || run.Ran != 4 || run.Fresh != 0 {
		t.Fatalf("actual retry execution facts changed: %+v %v rawB=%v", run, err, rawBPassed)
	}
	removed := "example.com/removedretry/b.TestB"
	if run.Outcomes[removed] != verify.TestNotRun || run.NoOutcome[removed] == "" {
		t.Fatalf("removed runnable retained its earlier passing witness: %+v", run)
	}
	if run.Outcomes["example.com/removedretry/a.TestA"] != verify.TestPassed || run.Outcomes["example.com/removedretry/c.TestC"] != verify.TestPassed || run.Uncached != 2 {
		t.Fatalf("removal poisoned unchanged current members or the durable initial result: %+v", run)
	}
	// The retained co-member must still reach native checking. Its own body
	// remains, but the package's test-variant guard moved. The unrelated A in
	// the same complete current inventory remains valid and grants above.
	kept := "example.com/removedretry/b.TestKeep"
	if run.Outcomes[kept] != verify.TestNotRun || !strings.Contains(run.NoOutcome[kept], gofresh.ReasonTestVariants) {
		t.Fatalf("removed batch member hid the remaining member's native judgment: %+v", run)
	}
	if attempts[removed] != 1 || attempts[kept] != 1 || attempts["example.com/removedretry/a.TestA"] != 1 || len(attempts) != 3 {
		t.Fatalf("removed member regained its spent retry budget: %v", attempts)
	}
}

func TestRunnableRemovalNeedsAnObservedPackageInventory(t *testing.T) {
	subject := gofresh.Subject{Package: "p", Symbol: "TestPresent"}
	for _, population := range []*runnableGeneration{
		nil,
		{},
		{packages: map[string]runnablePackage{"other": {}}},
		{packages: map[string]runnablePackage{"p": {runnables: []Obligation{{Package: "p", Kind: ObligationTest, Name: "TestPresent"}}}}},
	} {
		if err := observedRunnableRemoval(population, subject); err != nil {
			t.Fatalf("missing knowledge or a present runnable became removal: %v", err)
		}
	}
	complete := &runnableGeneration{packages: map[string]runnablePackage{"p": {}}}
	if err := observedRunnableRemoval(complete, subject); err == nil || !demonstratedDrift(err) {
		t.Fatalf("known empty current population did not establish removal: %v", err)
	}
}

func TestRetryCandidatesFollowActualRowsWithoutNativeEvidence(t *testing.T) {
	row := func(test string, pid int64) *stipulatorv1.TestResult {
		result := synthRow("plain", "p", test, passed)
		result.GetProducer().SetProcessId(pid)
		return result
	}
	wg := &witnessGroup{g: &captureGroup{}}
	unit := packageUnit{
		pkg: "p",
		run: packageRun{pkg: "p", disposition: healthy, tests: []*stipulatorv1.TestResult{row("TestA/sub", 1), row("TestA", 1)}},
		solos: []packageRun{
			{pkg: "p", disposition: healthy, tests: []*stipulatorv1.TestResult{row("TestA", 2)}, obs: &ProcessObservation{closeErr: errors.New("analysis unavailable")}},
			{pkg: "p", disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED, tests: []*stipulatorv1.TestResult{row("TestB", 3)}},
		},
	}
	merge := newExecMerge()
	candidates := collectRetryCandidates(wg, unit, merge, map[gofresh.Subject]uncacheable{})
	if len(candidates) != 2 || len(merge.denied) != 0 {
		t.Fatalf("native availability replaced actual grant eligibility: candidates=%+v denied=%v", candidates, merge.denied)
	}
	for i, candidate := range candidates {
		if candidate.group != wg || candidate.key.subject != (gofresh.Subject{Package: "p", Symbol: "TestA"}) || candidate.key.producer.pid != int64(i+1) || candidate.native != nil {
			t.Fatalf("attempt identity, top-level attribution or absent evidence changed: %+v", candidate)
		}
	}
}
