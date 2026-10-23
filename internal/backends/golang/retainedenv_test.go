package golang

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func TestRetainedEnvironmentIsSharedByCheckingAndExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"p.go": "package p\nfunc Answer() int{return 42}\n",
		"p_test.go": `package p
import ("encoding/json";"os";"testing")
func TestEnvironment(t *testing.T){
 if Answer()!=42{t.Fatal("answer")}
 data,err:=json.Marshal(os.Environ());if err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(os.Getenv("RECORDED_ENV"),data,0600);err!=nil{t.Fatal(err)}
}
`,
	})
	recorded := filepath.Join(n.Dir, "environment.json")
	n.WitnessEnv = gotool.SetEnv(witnessEnvOf(n), "RECORDED_ENV", recorded)
	n.WitnessEnv = gotool.SetEnv(n.WitnessEnv, "GOCOVERDIR", "ambient-cover")
	retainedSetFlags(n, "-coverprofile=coverage.txt")
	snapshot, err := rootsRunner.TakeEnvSnapshot(t.Context(), n.Dir, witnessEnvOf(n))
	if err != nil {
		t.Fatal(err)
	}
	old := commandHook
	spawns := 0
	commandHook = func(string, []string) { spawns++ }
	e, err := prepareRetainedEnvironment(n, n.Dir, snapshot.Value("GOROOT"))
	commandHook = old
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if spawns != 0 {
		t.Fatalf("prospective environment spawned %d commands", spawns)
	}
	got, _ := lookupEnv(e.Env, "GOCOVERDIR")
	if got == "ambient-cover" || got == "" || got != e.CoverageDir {
		t.Fatalf("warm environment borrowed ambient coverage: %q", got)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("warm coverage output is not a real directory: %v", err)
	}
	// This is the environment the caller may now bind for checking, before
	// any compiler or test artifact exists.
	admitted := slices.Clone(e.Env)
	profile, err := openRetainedCoverage(n)
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Close()
	a := retainedBuildOK(t, n, "retained.test")
	r, err := prepareRetainedRuntime(t.Context(), n, a, retainedRuntimeOptions{Package: "retained.test", Environment: e, GOCACHE: snapshot.Value("GOCACHE"), Coverage: profile}, retainedProcessHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if r.retainedEnvironment != e || !reflect.DeepEqual(r.Env, admitted) || r.CoverageDir != got {
		t.Fatal("execution replaced the checking environment")
	}
	result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if !result.HarnessComplete() || result.ProfileErr != nil {
		t.Fatalf("execution=%+v", result)
	}
	data, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatal(err)
	}
	var actual []string
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}
	slices.Sort(actual)
	slices.Sort(admitted)
	if !reflect.DeepEqual(actual, admitted) {
		t.Fatal("binary environment differs from warm binding")
	}
	retry, err := prepareRetainedEnvironment(n, n.Dir, snapshot.Value("GOROOT"))
	if err != nil {
		t.Fatal(err)
	}
	defer retry.Close()
	if retry.CoverageDir == e.CoverageDir {
		t.Fatal("isolation borrowed original attempt storage")
	}
}
