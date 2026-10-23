package golang

import (
	"os"
	"runtime"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestNativeWarmCoverageUsesProspectiveEnvironment(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and finalizes a covered environment-reading witness")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs", "REQ-go-retained-test-execution")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.com/coverageenv\n\ngo 1.26\n",
		"p_test.go": "package p\nimport (\"os\"; \"testing\")\nfunc TestEnv(t *testing.T) { if os.Getenv(\"GOCOVERDIR\") == \"\" { t.Fatal(\"coverage directory missing\") } }\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"."})
	cfg.SetPlainWitness(true)
	// Runtime-input drift must still refuse an explicitly asserted result;
	// this guard does not depend on coverage instrumentation's proof support.
	cfg.SetAssumePure(true)
	cfg.SetGoflags("-cover")
	n, err := NormalizeInvocation(t.Context(), dir, goInvocation("coverage", cfg))
	if err != nil {
		t.Fatal(err)
	}
	freezeWitnessAllocation(n, 1)
	first, err := prepareRetainedEnvironment(n, dir, n.GOROOT)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	// The inherited value deliberately equals the first real process's value.
	// Both builds use this unchanged compilation description. The next actual
	// process must still get a new private coverage directory, so an ambient
	// runtime binding would incorrectly accept the old observation.
	n.Env = gotool.SetEnv(n.Env, "GOCOVERDIR", first.CoverageDir)
	n.WitnessEnv = gotool.SetEnv(n.WitnessEnv, "GOCOVERDIR", first.CoverageDir)
	pc := &Capture{dir: dir, normalized: []*NormalizedInvocation{n}}
	d, err := pc.discover(t.Context())
	if err != nil || len(d.groups) != 1 {
		t.Fatalf("discovery: %+v %v", d, err)
	}
	snapshot, inputs, err := groupSnapshot(t.Context(), dir, d.groups[0])
	if err != nil {
		t.Fatal(err)
	}
	binding, err := snapshot.BindRuntime(t.Context(), gofresh.RuntimeDescription{Directory: first.Dir, Environment: first.Env})
	if err != nil {
		t.Fatal(err)
	}
	leg := &packageLeg{view: snapshot, binding: binding, environment: first, inputs: inputs}
	execution := &packageExecution{}
	const pkg = "example.com/coverageenv"
	run := runNativePackage(t.Context(), t.Context(), n, pkg, nil, 1, nil, leg, execution)
	defer run.attempt.release()
	if execution.built != nil && execution.built.Artifact != nil {
		defer execution.built.Artifact.Close()
	}
	finishNativeRun(t.Context(), &run)
	if run.disposition != healthy || run.obs == nil {
		t.Fatalf("actual covered run: %+v", run)
	}
	subject := gofresh.Subject{Package: pkg, Symbol: "TestEnv"}
	final, ok := run.obs.subject(subject)
	if !ok || final.Verdict.Status != gofresh.Valid || final.Fingerprint.PurityAssertion == "" {
		t.Fatalf("positive native producer: %+v close=%v", final, run.obs.closeErr)
	}
	warm, err := nativePackageLeg(t.Context(), snapshot, inputs, n, pkg)
	if err != nil {
		t.Fatal(err)
	}
	defer warm.dispose()
	if warm.environment == first || warm.environment.CoverageDir == first.CoverageDir {
		t.Fatal("another prospective process reused its predecessor's output storage")
	}
	verdict, err := warm.binding.Check(t.Context(), subject, final.Fingerprint)
	if err != nil || verdict.Status != gofresh.Stale {
		t.Fatalf("warm check accepted ambient GOCOVERDIR instead of its actual prospective value: %+v %v", verdict, err)
	}
}

func TestNativeSourceReleaseKeepsActiveRuntimeStorage(t *testing.T) {
	n := &NormalizedInvocation{Env: os.Environ(), GOFLAGS: "-cover"}
	environment, err := prepareRetainedEnvironment(n, t.TempDir(), runtime.GOROOT())
	if err != nil {
		t.Fatal(err)
	}
	leg := &packageLeg{environment: environment}
	defer leg.dispose()
	leg.release()
	if _, err := os.Stat(environment.CoverageDir); err != nil {
		t.Fatalf("a sibling's source degradation removed an active process's storage: %v", err)
	}
	leg.dispose()
	if _, err := os.Stat(environment.CoverageDir); !os.IsNotExist(err) {
		t.Fatalf("completed operation retained runtime storage: %v", err)
	}
}
