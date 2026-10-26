package golang

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestRetryConstructionPGODriftRevokesEarlierGrant(t *testing.T) {
	if testing.Short() {
		t.Skip("executes an auto-PGO metadata-motion cycle")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-revalidation", "REQ-evidence-witness-freshness-inputs")
	neutralAmbient(t)
	b := "package b\nimport (\"os\"; \"testing\")\n//gofresh:pure\nfunc TestB(t *testing.T) { value, err := os.ReadFile(\"trigger\"); if err != nil { t.Fatal(err) }; if string(value) == \"yes\" { if err := os.WriteFile(\"../a/data\", []byte(\"moved\"), 0600); err != nil { t.Fatal(err) } } }\n"
	a := fmt.Sprintf("package a\nimport (\"os\"; \"testing\")\n//gofresh:pure\nfunc TestA(t *testing.T) { value, err := os.ReadFile(\"data\"); if err != nil { t.Fatal(err) }; if string(value) == \"moved\" { if err := os.WriteFile(\"../b/b_test.go\", []byte(%q), 0600); err != nil { t.Fatal(err) } } }\n", strings.Replace(b, "package b", "package main", 1))
	c := "package c\nimport (\"os\"; \"testing\")\nconst armed = false\n//gofresh:pure\nfunc TestC(t *testing.T) { if armed { if err := os.WriteFile(\"../b/trigger\", []byte(\"yes\"), 0600); err != nil { t.Fatal(err) } } }\n"
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/pgowaves\n\ngo 1.26\n", "a/data": "old", "b/trigger": "no", "a/a_test.go": a, "b/b_test.go": b, "c/c_test.go": c})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetPlainWitness(true)
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("plain", cfg)})
	writePolicyRecord(t, dir, policy)
	if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Uncached != 0 {
		t.Fatalf("cold: %+v %v", run, err)
	}
	if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Fresh != 3 {
		t.Fatalf("warm: %+v %v", run, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c/c_test.go"), []byte(strings.Replace(c, "armed = false", "armed = true", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	rawB := false
	prior := afterRetryWaveForTest
	afterRetryWaveForTest = func(merge *execMerge) {
		for _, row := range merge.rows {
			if row.GetPackage() == "example.com/pgowaves/b" && row.GetTest() == "TestB" {
				rawB = row.GetOutcome() == passed && merge.disp[keyOfProducer(row.GetProducer())] == healthy
			}
		}
	}
	t.Cleanup(func() { afterRetryWaveForTest = prior })
	run, err := RunWitnesses(t.Context(), dir, noSeeding{})
	if err != nil || !rawB || run.Ran != 3 || run.Fresh != 0 {
		t.Fatalf("actual retry facts: %+v %v rawB=%v", run, err, rawB)
	}
	key := "example.com/pgowaves/b.TestB"
	if run.Outcomes[key] != verify.TestNotRun || run.NoOutcome[key] == "" {
		t.Fatalf("typed construction drift retained B's earlier passing grant: %+v", run)
	}
}

func TestRetryGenericConstructionFailureKeepsMeasuredPass(t *testing.T) {
	if testing.Short() {
		t.Skip("refuses a checking construction after a real passing retry")
	}
	neutralAmbient(t)
	writer := "package writer\nimport (\"os\"; \"testing\")\nconst armed = false\n//gofresh:pure\nfunc TestWriter(t *testing.T) { if armed { if err := os.WriteFile(\"../reader/data\", []byte(\"new\"), 0600); err != nil { t.Fatal(err) } } }\n"
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/unknowncheck\n\ngo 1.26\n", "reader/data": "old", "reader/r_test.go": "package reader\nimport (\"os\"; \"testing\")\n//gofresh:pure\nfunc TestRead(t *testing.T) { if _, err := os.ReadFile(\"data\"); err != nil { t.Fatal(err) } }\n", "writer/w_test.go": writer})
	retryLifecyclePolicy(t, dir)
	if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Uncached != 0 {
		t.Fatalf("cold: %+v %v", run, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "writer/w_test.go"), []byte(strings.Replace(writer, "armed = false", "armed = true", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	pc, err := LoadCapture(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := pc.discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rawPass := false
	prior := afterRetryWaveForTest
	afterRetryWaveForTest = func(merge *execMerge) {
		for _, row := range merge.rows {
			if row.GetPackage() == "example.com/unknowncheck/reader" && row.GetTest() == "TestRead" {
				rawPass = row.GetOutcome() == passed && merge.disp[keyOfProducer(row.GetProducer())] == healthy
			}
		}
		// Refuse the subsequent analysis construction, without changing the
		// immutable descriptions the completed processes actually executed.
		for _, group := range discovery.groups {
			group.witnessEnv = []string{"malformed-environment"}
		}
	}
	t.Cleanup(func() { afterRetryWaveForTest = prior })
	run, err := RunWitnessesPolicy(t.Context(), pc, noSeeding{})
	if err != nil || !rawPass || run.Outcomes["example.com/unknowncheck/reader.TestRead"] != verify.TestPassed || run.NoOutcome["example.com/unknowncheck/reader.TestRead"] != "" || run.Uncached != 1 {
		t.Fatalf("generic construction failure became demonstrated drift: %+v %v raw=%v", run, err, rawPass)
	}
}
