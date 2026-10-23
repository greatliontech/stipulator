package golang

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestLinkerInputFilesUseNativeEffectiveEligibility(t *testing.T) {
	stipulate.Covers(t, "REQ-go-invocation-input-files")
	neutralAmbient(t)
	dir, out := discoverFixture(t), linkerTempDir(t)
	cache, modules := filepath.Join(out, "cache"), filepath.Join(out, "modules")
	put := func(path string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("retained-input"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	config := filepath.Join(out, "goenv")
	if err := os.WriteFile(config, []byte("GOCACHE="+cache+"\nGOMODCACHE="+modules+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", config)
	t.Setenv("GOCACHE", "")
	t.Setenv("GOMODCACHE", "")
	snapshot, err := ownedRunner.TakeEnvSnapshot(t.Context(), dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Value("GOCACHE") != cache || snapshot.Value("GOMODCACHE") != modules {
		t.Fatal("persisted cache selections inactive")
	}
	check := func(file string, overrides ...string) error {
		t.Helper()
		c := linkerConfig(t, `packages:"./..." input_files_from_environment:"MANIFEST"`)
		c.SetEnvironment(append([]string{"MANIFEST=" + file}, overrides...))
		n, err := NormalizeInvocation(t.Context(), dir, goInvocation("test", c))
		if err != nil {
			t.Fatal(err)
		}
		e, err := prepareRetainedEnvironment(n, dir, n.GOROOT)
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		return requireExactInputEligibility(t.Context(), n, e)
	}
	for _, tc := range []struct {
		name, path string
		refuse     bool
	}{
		{"effective-build-cache", filepath.Join(cache, "manifest.json"), true},
		{"effective-module-cache", filepath.Join(modules, "example.com/m@v1.0.0", "manifest.json"), true},
		{"build-cache-fuzz-exception", filepath.Join(cache, "fuzz", "manifest.json"), false},
		{"module-cache-download-exception", filepath.Join(modules, "cache", "manifest.json"), false},
		{"ordinary-external", filepath.Join(out, "retained", "manifest.json"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := check(put(tc.path))
			if tc.refuse {
				if err == nil || !strings.Contains(err.Error(), "MANIFEST") || !strings.Contains(err.Error(), "guard-covered") {
					t.Fatalf("cache classification: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	file := put(filepath.Join(cache, "other.json"))
	other := filepath.Join(out, "other-cache")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := check(file, "GOCACHE="+other); err != nil {
		t.Fatalf("effective override ignored: %v", err)
	}
	file = put(filepath.Join(modules, "cache", "nested-build", "manifest.json"))
	if err := check(file, "GOCACHE="+filepath.Dir(file)); err == nil || !strings.Contains(err.Error(), "guard-covered") {
		t.Fatalf("overlapping root admitted: %v", err)
	}
	versionFile, err := filepath.EvalSymlinks(filepath.Join(snapshot.Value("GOROOT"), "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path, cause string }{
		{"toolchain-file", versionFile, "guard-covered"},
		{"machine-projection", "/proc/cpuinfo", "projection"},
		{"volatile-region", "/proc/stat", "volatile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name != "toolchain-file" && runtime.GOOS != "linux" {
				t.Skip("Linux classification")
			}
			if err := check(tc.path); err == nil || !strings.Contains(err.Error(), tc.cause) {
				t.Fatalf("native cause lost: %v", err)
			}
		})
	}
}

func TestLinkerInputEligibilityRechecksFrozenAssociationsAndFiles(t *testing.T) {
	stipulate.Covers(t, "REQ-go-invocation-input-files")
	neutralAmbient(t)
	root, outside := discoverFixture(t), linkerTempDir(t)
	file := filepath.Join(outside, "manifest")
	if err := os.WriteFile(file, []byte("manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	n := &NormalizedInvocation{Dir: root, Env: gotool.SetEnv(os.Environ(), "MANIFEST", file)}
	if err := resolveLinkerSelection(n, linkerConfig(t, `input_files_from_environment:"MANIFEST"`)); err != nil {
		t.Fatal(err)
	}
	e := linkerRuntime(t, n)
	if err := requireExactInputEligibility(t.Context(), n, e); err != nil {
		t.Fatal(err)
	}
	e.Env = gotool.SetEnv(e.Env, "MANIFEST", file+"-other")
	if err := requireExactInputEligibility(t.Context(), n, e); err == nil || !strings.Contains(err.Error(), "no longer selects") {
		t.Fatalf("association lost: %v", err)
	}
	e = linkerRuntime(t, n)
	n.ExcludedPaths = []string{outside}
	if err := requireExactInputEligibility(t.Context(), n, e); err == nil {
		t.Fatal("excluded file admitted")
	}
	// Root-listing exclusions do not exclude descendants.
	n.ExcludedPaths = []string{string(filepath.Separator)}
	if err := requireExactInputEligibility(t.Context(), n, e); err != nil {
		t.Fatal(err)
	}
	n.ExcludedPaths = nil
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := requireExactInputEligibility(t.Context(), n, e); err == nil {
		t.Fatal("missing file admitted after initial eligibility")
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if err := requireExactInputEligibility(t.Context(), n, e); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory widened selection: %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file+"-target", []byte("manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file+"-target", file); err != nil {
		t.Fatal(err)
	}
	if err := requireExactInputEligibility(t.Context(), n, e); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink admitted: %v", err)
	}
}

func TestLinkerInputGatePreventsEveryDegradedNativeAttempt(t *testing.T) {
	if testing.Short() {
		t.Skip("builds retained artifact and verifies no binary starts")
	}
	stipulate.Covers(t, "REQ-go-invocation-input-files")
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": "package p\nimport \"testing\"\nfunc TestOK(t *testing.T){}\n"})
	file := filepath.Join(linkerTempDir(t), "manifest")
	write := func() {
		t.Helper()
		if err := os.WriteFile(file, []byte("manifest"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	n.Env = gotool.SetEnv(n.Env, "GOCOVERDIR", file)
	n.WitnessEnv = gotool.SetEnv(n.WitnessEnv, "GOCOVERDIR", file)
	if err := resolveLinkerSelection(n, linkerConfig(t, `input_files_from_environment:"GOCOVERDIR"`)); err != nil {
		t.Fatal(err)
	}
	// Establish that the selected regular file itself is eligible; coverage
	// rewrites its selecting variable, which must not rebind the declaration.
	e, err := prepareRetainedEnvironment(n, n.Dir, n.GOROOT)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireExactInputEligibility(t.Context(), n, e); err != nil {
		t.Fatal(err)
	}
	_ = e.Close()
	retainedSetFlags(n, "-cover")
	a := retainedBuildOK(t, n, "retained.test")
	execution := &packageExecution{built: &retainedBuildResult{Artifact: a}}
	spawns := 0
	oldHook := commandHook
	t.Cleanup(func() { commandHook = oldHook })
	commandHook = func(name string, _ []string) {
		if name == a.Path {
			spawns++
		}
	}
	for i := 0; i < 2; i++ {
		run := runNativePackage(t.Context(), t.Context(), n, "retained.test", []string{"TestOK"}, int32(i+1), nil, nil, execution)
		if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED || run.producer != nil || run.obs != nil || len(run.diags) != 1 || !strings.Contains(run.diags[0].GetOutput(), "no longer selects") {
			t.Fatalf("coverage mismatch was not execution refusal: %+v", run)
		}
	}
	if spawns != 0 {
		t.Fatalf("invalid selection started %d binaries", spawns)
	}
	// The same unconditional gate refuses a file replaced between attempts,
	// even with no native leg and a reusable already-built artifact.
	retainedSetFlags(n, "")
	for _, replacement := range []string{"missing", "directory"} {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if replacement == "directory" {
			if err := os.Mkdir(file, 0700); err != nil {
				t.Fatal(err)
			}
		}
		run := runNativePackage(t.Context(), t.Context(), n, "retained.test", []string{"TestOK"}, 3, nil, nil, execution)
		if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED || run.producer != nil || len(run.diags) != 1 || !strings.Contains(run.diags[0].GetOutput(), "GOCOVERDIR") {
			t.Fatalf("%s selected file executed: %+v", replacement, run)
		}
		if replacement == "directory" {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
		}
		write()
	}
	if spawns != 0 {
		t.Fatalf("mutated selection started %d binaries", spawns)
	}
	// A valid attempt traverses the same seam, proving the zero-spawn checks
	// count retained binaries rather than only Go commands or resolver children.
	n.InputFiles = nil
	retainedSetFlags(n, "-cover")
	run := runNativePackage(t.Context(), t.Context(), n, "retained.test", []string{"TestOK"}, 4, nil, nil, execution)
	if run.attempt != nil {
		defer run.attempt.release()
	}
	if spawns != 1 || run.disposition != healthy {
		t.Fatalf("positive spawn control: %d %+v", spawns, run)
	}
}
