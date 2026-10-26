package golang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestNativePackageRosterAndPWDPublishAndServe(t *testing.T) {
	if testing.Short() {
		t.Skip("executes native package producers and warm checks")
	}
	stipulate.Covers(t, "REQ-evidence-outcome-premises", "REQ-evidence-witness-freshness-purity", "REQ-evidence-witness-freshness-inputs", "REQ-go-retained-test-execution")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/native\n\ngo 1.26\n", "pkg/data": "fixture"})
	source := fmt.Sprintf(`package pkg
import ("os"; "testing")
func TestFirst(t *testing.T) { if os.Getenv("PWD") != %q { t.Fatal("wrong process PWD") } }
func TestSecond(t *testing.T) { if os.Getenv("PWD") != %q { t.Fatal("wrong process PWD") } }
func Example() {
// Output:
}
`, filepath.Join(dir, "pkg"), filepath.Join(dir, "pkg"))
	if err := os.WriteFile(filepath.Join(dir, "pkg", "native_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./pkg"})
	cfg.SetPlainWitness(true)
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("native", cfg)})
	writePolicyRecord(t, dir, policy)
	report, run, err := ExecutePolicyWitnessed(t.Context(), mustCapture(t, t.Context(), dir, policy), noSeeding{})
	if err != nil || !SuiteHealthy(report) || run.Ran != 2 || run.Uncached != 0 {
		t.Fatalf("cold: report=%v run=%+v err=%v", report, run, err)
	}
	observations := report.GetObservations()
	if len(observations) != 1 || observations[0].GetCompleted() == nil {
		t.Fatalf("process account: %v", observations)
	}
	method, subjects := outcomeFields(t, observations[0].GetCompleted().GetManifest())
	if method == "" || len(subjects) != 3 {
		t.Fatalf("whole actual roster missing: method=%q subjects=%v", method, subjects)
	}
	records := witnesscache.Load(t.Context(), dir).Records
	if len(records) != 2 {
		t.Fatalf("records=%d", len(records))
	}
	for _, rec := range records {
		description, err := runtimeinput.Describe(rec.Fingerprint.RuntimeInputs, dir)
		if err != nil || !slices.Contains(description.EnvNames, "PWD") {
			t.Fatalf("stored PWD absent: %+v %v", description, err)
		}
		if rec.Fingerprint.PurityAssertion != "" || rec.Fingerprint.RuntimeInputs != observations[0].GetCompleted().GetManifest() {
			t.Fatal("record lost its actual process observation or gained purity")
		}
	}
	warm, err := RunWitnesses(t.Context(), dir, noSeeding{})
	if err != nil || warm.Fresh != 2 || warm.Ran != 0 {
		t.Fatalf("warm package PWD: %+v %v", warm, err)
	}
	// A non-publishable sibling still belongs to the actual process. Its file
	// outcome has no admitted method, so neither environment reader can borrow
	// the supported subset's proof; all actual passing outcomes remain true.
	source += "\nfunc TestFile(t *testing.T) { if _, err := os.ReadFile(\"data\"); err != nil { t.Fatal(err) } }\n"
	if err := os.WriteFile(filepath.Join(dir, "pkg", "native_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	report, run, err = ExecutePolicyWitnessed(t.Context(), mustCapture(t, t.Context(), dir, policy), noSeeding{})
	if err != nil || !SuiteHealthy(report) || run.Ran != 3 || run.Uncached != 3 {
		t.Fatalf("unsupported neighbor: %+v %v report=%v", run, err, report)
	}
	for _, name := range []string{"TestFirst", "TestSecond", "TestFile"} {
		if run.Outcomes["example.com/native/pkg."+name] != verify.TestPassed {
			t.Fatalf("lost %s outcome", name)
		}
	}
	guard := report.GetObservations()[0].GetCompleted()
	method, subjects = outcomeFields(t, guard.GetManifest())
	if method != "" || len(subjects) != 0 || guard.GetOutcomeReason() == "" {
		t.Fatalf("unsupported neighbor gained support: %v", guard)
	}
}

func TestNativeIsolationKeepsAttemptEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("executes an original failed package and its isolated witness")
	}
	stipulate.Covers(t, "REQ-evidence-outcome-premises", "REQ-evidence-witness-freshness-isolation", "REQ-go-retained-test-execution")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.com/nativeisolation\n\ngo 1.26\n",
		"p_test.go": "package p\nimport (\"os\"; \"testing\")\nfunc TestEnv(t *testing.T) { if os.Getenv(\"PWD\") == \"\" { t.Fatal(\"missing PWD\") } }\nfunc TestFail(t *testing.T) { t.Fatal(\"ordinary failure\") }\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"."})
	cfg.SetPlainWitness(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("native", cfg)})
	writePolicyRecord(t, dir, p)
	var accounts []gofresh.ProcessAccount
	prior := closeNativeProcess
	closeNativeProcess = func(ctx context.Context, unit *gofresh.PackageUnit) (gofresh.FinalizedProcess, error) {
		final, err := prior(ctx, unit)
		account, _ := unit.Account()
		accounts = append(accounts, account)
		return final, err
	}
	t.Cleanup(func() { closeNativeProcess = prior })
	run, err := RunWitnesses(t.Context(), dir, noSeeding{})
	if err != nil || run.Outcomes["example.com/nativeisolation.TestEnv"] != verify.TestPassed || run.Outcomes["example.com/nativeisolation.TestFail"] != verify.TestFailed {
		t.Fatalf("outcomes: %+v %v", run, err)
	}
	if len(accounts) != 2 || accounts[0].Process == accounts[1].Process {
		t.Fatalf("attempt identity: %+v", accounts)
	}
	if accounts[0].Terminal.Kind != gofresh.NormalCompletion || accounts[1].Terminal.Kind != gofresh.NormalCompletion {
		t.Fatalf("ordinary failure lost harness completion: %+v", accounts)
	}
	for i, want := range []int{2, 1} {
		method, subjects := outcomeFields(t, accounts[i].Inputs.Manifest)
		if method == "" || len(subjects) != want {
			t.Fatalf("attempt %d borrowed a roster: %q %v", i, method, subjects)
		}
	}
	records := witnesscache.Load(t.Context(), dir).Records
	if len(records) != 1 || records[0].Test != "TestEnv" || records[0].Fingerprint.RuntimeInputs != accounts[1].Inputs.Manifest || records[0].Fingerprint.RuntimeInputs == accounts[0].Inputs.Manifest {
		t.Fatalf("publication borrowed original evidence: %+v", records)
	}
}

func TestNativeZeroRunnablePackagesKeepBuildAndProcessHealth(t *testing.T) {
	if testing.Short() {
		t.Skip("executes testless and zero-runnable package health")
	}
	stipulate.Covers(t, "REQ-go-policy-complete", "REQ-go-retained-test-execution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./buildonly", "./notest"})
	health, rows, diagnostics, observations := executeInvocationObserved(t, time.Minute, cfg, "empty")
	if health.GetDisposition() != healthy || len(rows) != 0 || len(diagnostics) != 0 {
		t.Fatalf("empty package health: %v rows=%v diagnostics=%v", health, rows, diagnostics)
	}
	if len(observations) != 1 || observations[0].Wire.GetPackage() != "example.com/exec/notest" || observations[0].Wire.GetProducer().GetProcessId() <= 0 {
		t.Fatalf("testless package fabricated a process or actual empty binary disappeared: %v", observations)
	}
	if observations[0].Wire.GetCompleted() != nil || observations[0].Wire.GetIncompleteReason() == "" || len(observations[0].final.Subjects) != 0 {
		t.Fatalf("zero runnable package fabricated native subject evidence: %+v", observations[0])
	}
}
