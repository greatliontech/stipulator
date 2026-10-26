package golang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestNativeSnapshotIncludesRunnablesAddedAfterPolicyDiscovery(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a package whose runnable population changed before snapshot capture")
	}
	stipulate.Covers(t, "REQ-evidence-outcome-premises", "REQ-go-retained-test-execution", "REQ-check-derivation")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.com/generation\n\ngo 1.26\n",
		"p_test.go": "package p\nimport (\"os\"; \"testing\")\nfunc TestEnv(t *testing.T) { if os.Getenv(\"PWD\") == \"\" { t.Fatal(\"PWD\") } }\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"."})
	cfg.SetPlainWitness(true)
	cfg.SetPgo("off")
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("plain", cfg)})
	pc := mustCapture(t, t.Context(), dir, policy)
	if err := pc.ConfigureExecution(true, nil); err != nil {
		t.Fatal(err)
	}
	discovered, err := pc.discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before := append([]Obligation(nil), discovered.invocations[0].obligations...)
	prior := beforeGroupEngineForTest
	beforeGroupEngineForTest = func() {
		if err := os.WriteFile(filepath.Join(dir, "clock_test.go"), []byte("package p\nimport (\"testing\"; \"time\")\nfunc TestClock(t *testing.T) { if time.Now().IsZero() { t.Fatal(\"clock\") } }\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { beforeGroupEngineForTest = prior })
	var declared []gofresh.FinalizedSubject
	priorClose := closeNativeProcess
	closeNativeProcess = func(ctx context.Context, unit *gofresh.PackageUnit) (gofresh.FinalizedProcess, error) {
		final, err := priorClose(ctx, unit)
		declared = final.Subjects
		return final, err
	}
	t.Cleanup(func() { closeNativeProcess = priorClose })
	report, run, err := ExecutePolicyWitnessed(t.Context(), pc, noSeeding{})
	if err != nil || !SuiteHealthy(report) || run.Ran != 2 || run.Outcomes["example.com/generation.TestEnv"] != verify.TestPassed || run.Outcomes["example.com/generation.TestClock"] != verify.TestPassed {
		t.Fatalf("actual execution changed: %+v %v report=%v", run, err, report)
	}
	if run.Uncached != 2 || len(witnesscache.Load(t.Context(), dir).Records) != 0 {
		t.Fatalf("new unsupported runnable borrowed the old population's proof: %+v", run)
	}
	if len(declared) != 2 {
		t.Fatalf("native registration omitted an actual runnable: %+v", declared)
	}
	if fmt.Sprint(before) != fmt.Sprint(discovered.invocations[0].obligations) {
		t.Fatal("native roster derivation rewrote the accepted policy obligations")
	}
}

func TestNativeDriftRetryRefreshesRunnablePopulation(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a drift retry after adding an unsupported runnable")
	}
	stipulate.Covers(t, "REQ-evidence-outcome-premises", "REQ-evidence-witness-freshness-revalidation")
	neutralAmbient(t)
	clock := "package reader\nimport (\"testing\"; \"time\")\nfunc TestClock(t *testing.T) { if time.Now().IsZero() { t.Fatal(\"clock\") } }\n"
	writer := fmt.Sprintf("package writer\nimport (\"os\"; \"testing\")\nconst armed = false\n//gofresh:pure\nfunc TestWriter(t *testing.T) { if armed { if err := os.WriteFile(\"../reader/clock_test.go\", []byte(%q), 0600); err != nil { t.Fatal(err) } } }\n", clock)
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/retryroster\n\ngo 1.26\n",
		"reader/r_test.go": "package reader\nimport (\"os\"; \"testing\")\nfunc TestEnv(t *testing.T) { if os.Getenv(\"PWD\") == \"\" { t.Fatal(\"PWD\") } }\n",
		"writer/w_test.go": writer,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetPlainWitness(true)
	cfg.SetPgo("off")
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("plain", cfg)})
	run := func() *verify.TestRun {
		pc := mustCapture(t, t.Context(), dir, policy)
		// Exercise the directly supplied normalized execution configuration;
		// ordinary policy authoring still refuses raw selection overrides.
		pc.normalized[0].Args = []string{"--test.run=."}
		result, err := RunWitnessesPolicy(t.Context(), pc, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if cold := run(); cold.Ran != 2 || cold.Uncached != 0 {
		t.Fatalf("cold: %+v", cold)
	}
	if warm := run(); warm.Fresh != 2 || warm.Ran != 0 {
		t.Fatalf("warm: %+v", warm)
	}
	original := cacheRecord(t, witnesscache.Load(t.Context(), dir).Records, "example.com/retryroster/reader", "TestEnv")
	if original == nil {
		t.Fatal("no reader record")
	}
	if err := os.WriteFile(filepath.Join(dir, "writer/w_test.go"), []byte(strings.Replace(writer, "armed = false", "armed = true", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	var reader gofresh.FinalizedProcess
	prior := closeNativeProcess
	closeNativeProcess = func(ctx context.Context, unit *gofresh.PackageUnit) (gofresh.FinalizedProcess, error) {
		final, err := prior(ctx, unit)
		for _, result := range final.Subjects {
			if result.Subject.Package == "example.com/retryroster/reader" {
				reader = final
				break
			}
		}
		return final, err
	}
	t.Cleanup(func() { closeNativeProcess = prior })
	result := run()
	if result.Ran != 3 || result.Fresh != 0 || result.Outcomes["example.com/retryroster/reader.TestClock"] != verify.TestPassed {
		t.Fatalf("retry did not execute the actual widened population: %+v", result)
	}
	if len(reader.Subjects) != 2 || reader.OutcomeReason == "" || result.Uncached != 2 {
		t.Fatalf("retry used the earlier generation's population: result=%+v process=%+v", result, reader)
	}
	for _, record := range witnesscache.Load(t.Context(), dir).Records {
		if record.Package == original.Package && record.Test == original.Test && record.Fingerprint != original.Fingerprint {
			t.Fatal("new unsupported neighbor permitted a supported retry deposit")
		}
	}
}

func TestNativeRosterMotionCannotCrossCaptureOrRegistration(t *testing.T) {
	if testing.Short() {
		t.Skip("changes runnable membership across the two native preparation boundaries")
	}
	stipulate.Covers(t, "REQ-evidence-outcome-premises", "REQ-go-retained-test-execution")
	neutralAmbient(t)
	for _, duringCapture := range []bool{true, false} {
		name := "before-registration"
		if duringCapture {
			name = "inside-capture"
		}
		t.Run(name, func(t *testing.T) {
			dir := writeModule(t, map[string]string{"go.mod": "module example.com/movingroster\n\ngo 1.26\n", "p_test.go": "package p\nimport (\"os\"; \"testing\")\nfunc TestEnv(t *testing.T) { if os.Getenv(\"PWD\") == \"\" { t.Fatal(\"PWD\") } }\n"})
			cfg := &stipulatorv1.GoInvocationConfig{}
			cfg.SetPackages([]string{"."})
			cfg.SetPlainWitness(true)
			cfg.SetPgo("off")
			policy := &stipulatorv1.TestPolicy{}
			policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("plain", cfg)})
			move := func() {
				if err := os.WriteFile(filepath.Join(dir, "extra_test.go"), []byte("package p\nimport \"testing\"\nfunc TestExtra(t *testing.T) {}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			oldCapture, oldBuild := afterRunnableSnapshotForTest, beforePackageBuildForTest
			t.Cleanup(func() { afterRunnableSnapshotForTest, beforePackageBuildForTest = oldCapture, oldBuild })
			if duringCapture {
				afterRunnableSnapshotForTest = move
			} else {
				beforePackageBuildForTest = func(string) { move() }
			}
			report, run, err := ExecutePolicyWitnessed(t.Context(), mustCapture(t, t.Context(), dir, policy), noSeeding{})
			if err != nil || !SuiteHealthy(report) || run.Ran != 2 || run.Uncached != 2 || len(witnesscache.Load(t.Context(), dir).Records) != 0 {
				t.Fatalf("runnable motion crossed preparation or changed execution: %+v %v report=%v", run, err, report)
			}
		})
	}
}

func TestNativeFreshBodyAndHelperGenerationRetainsProof(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a fresh retry generation with changed body and helper code")
	}
	stipulate.Covers(t, "REQ-evidence-outcome-premises", "REQ-evidence-witness-freshness-revalidation")
	neutralAmbient(t)
	initial := "package reader\nimport (\"os\"; \"testing\")\nfunc TestEnv(t *testing.T) { if os.Getenv(\"PWD\") == \"\" { t.Fatal(\"PWD\") } }\n"
	replacement := "package reader\nimport (\"os\"; \"testing\")\nfunc currentDirectory() string { return os.Getenv(\"PWD\") }\nfunc TestEnv(t *testing.T) { if currentDirectory() == \"\" { t.Fatal(\"changed body\") } }\n"
	writer := fmt.Sprintf("package writer\nimport (\"os\"; \"testing\")\nconst armed = false\n//gofresh:pure\nfunc TestWriter(t *testing.T) { if armed { if err := os.WriteFile(\"../reader/r_test.go\", []byte(%q), 0600); err != nil { t.Fatal(err) } } }\n", replacement)
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/bodygeneration\n\ngo 1.26\n", "reader/r_test.go": initial, "writer/w_test.go": writer})
	retryLifecyclePolicy(t, dir)
	if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Uncached != 0 {
		t.Fatalf("cold: %+v %v", run, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "writer/w_test.go"), []byte(strings.Replace(writer, "armed = false", "armed = true", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	run, err := RunWitnesses(t.Context(), dir, noSeeding{})
	if err != nil || run.Ran != 2 || run.Fresh != 0 || run.Uncached != 0 || run.Outcomes["example.com/bodygeneration/reader.TestEnv"] != verify.TestPassed {
		t.Fatalf("fresh same-population generation refused: %+v %v", run, err)
	}
	warm, err := RunWitnesses(t.Context(), dir, noSeeding{})
	if err != nil || warm.Fresh != 2 || warm.Ran != 0 {
		t.Fatalf("fresh generation did not retain reusable evidence: %+v %v", warm, err)
	}
}
