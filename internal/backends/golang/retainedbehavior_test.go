package golang

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func TestRetainedActualFlagParsingAndLiteralSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": `package p
import ("flag"; "testing")
func TestA(t *testing.T){if flag.Lookup("test.parallel").Value.String()!="3"{t.Fatal("raw precedence")};if len(flag.Args())!=2||flag.Args()[0]!="positional"{t.Fatal("positional stop",flag.Args())}}
func TestB(t *testing.T){t.Fatal("selection widened")}
`})
	n.Count = 2
	n.Args = []string{"-test.parallel", "3", "positional", "-test.parallel=9"}
	retainedSetFlags(n, "-parallel=1 -test.count=9 -test.run=TestB -test.timeout=1ns")
	a := retainedBuildOK(t, n, "retained.test")
	r := retainedRuntimeFor(t, n, a, "retained.test", []string{"TestA"}, nil)
	result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if !result.HarnessComplete() || len(result.Stream.tests) != 2 {
		t.Fatalf("flag parse=%+v stream=%+v", result, result.Stream)
	}
	for _, test := range result.Stream.tests {
		if test.GetTest() != "TestA" || test.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
			t.Fatalf("unexpected outcome: %v", test)
		}
	}
	r = retainedRuntimeFor(t, n, a, "retained.test", []string{}, nil)
	result = runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if !result.HarnessComplete() || len(result.Stream.tests) != 0 {
		t.Fatalf("explicit empty selection executed runnables: %+v stream=%+v", result, result.Stream)
	}
	n.Args = []string{"-unknown-retained-flag"}
	r = retainedRuntimeFor(t, n, a, "retained.test", []string{"TestA"}, nil)
	result = runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if result.BinaryState == nil || result.BinaryState.ExitCode() != 2 || result.HarnessComplete() {
		t.Fatalf("unknown raw flag was reinterpreted: %+v", result)
	}
}

func TestRetainedCompilationKeepsItsCompleteEnvironment(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"sub/p_test.go": "package sub\nimport \"testing\"\nfunc TestOK(t *testing.T){}\n"})
	n.WitnessEnv = gotool.SetEnv(witnessEnvOf(n), "COMPILATION_MARKER", "complete value with spaces")
	want, err := gotool.EnvForCommand(n.WitnessEnv, n.Dir)
	if err != nil {
		t.Fatal(err)
	}
	old := ownedRunner.Prepare
	defer func() { ownedRunner.Prepare = old }()
	seen := false
	ownedRunner.Prepare = func(cmd *exec.Cmd) {
		if old != nil {
			old(cmd)
		}
		if len(cmd.Args) > 1 && cmd.Args[1] == "test" {
			seen = true
			if cmd.Dir != n.Dir || !reflect.DeepEqual(cmd.Env, want) {
				t.Errorf("compiler description moved to runtime: dir=%q or complete environment differs", cmd.Dir)
			}
		}
	}
	a := retainedBuildOK(t, n, "retained.test/sub")
	if !seen {
		t.Fatal("compiler did not use owning runner")
	}
	r := retainedRuntimeFor(t, n, a, "retained.test/sub", nil, nil)
	actual, err := gotool.EnvForCommand(r.Env, r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, r.Env) {
		t.Fatal("prepared runtime environment differs from spawn normalization")
	}
	if r.Dir == n.Dir || reflect.DeepEqual(r.Env, want) {
		t.Fatal("compilation description substituted for runtime")
	}
}

func TestRetainedBinaryDeadlineIsActualBinaryFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": "package p\nimport (\"testing\";\"time\")\nfunc TestHang(t *testing.T){for{time.Sleep(time.Second)}}\n"})
	n.Args = []string{"-test.timeout=25ms"}
	a := retainedBuildOK(t, n, "retained.test")
	r := retainedRuntimeFor(t, n, a, "retained.test", nil, nil)
	result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if result.Interrupted || result.HarnessComplete() || result.BinaryState == nil || result.BinaryState.ExitCode() != 2 || result.ConversionErr != nil {
		t.Fatalf("deadline=%+v", result)
	}
	if got := result.Classify(n, "retained.test"); got.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
		t.Fatalf("deadline health=%v", got.disposition)
	}
}

func TestRetainedOrdinaryExecutionNeedsNoNewTemporaryStorage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": "package p\nimport \"testing\"\nfunc TestOK(t *testing.T){}\n"})
	a := retainedBuildOK(t, n, "retained.test")
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	r := retainedRuntimeFor(t, n, a, "retained.test", nil, nil)
	result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if !result.HarnessComplete() {
		t.Fatalf("ordinary execution required scratch or proof: %+v", result)
	}
}
