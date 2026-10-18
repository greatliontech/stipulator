package golang

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/stipulator/stipulate"
)

// This is an actual effective-cache fixture, not a string-prefix surrogate.
// The old consumer accepts the cache file even though native ingest elides it.
func TestLinkerInputFilesUseNativeEffectiveEligibility(t *testing.T) {
	stipulate.Covers(t, "REQ-go-invocation-input-files")
	neutralAmbient(t)
	dir := discoverFixture(t)
	out := t.TempDir()
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
	reader := gotool.NewEnvReader(rootsRunner, dir, os.Environ())
	snapshot, err := reader.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Value("GOCACHE") != cache || snapshot.Value("GOMODCACHE") != modules {
		t.Fatalf("GOENV controls inactive: GOCACHE=%q GOMODCACHE=%q", snapshot.Value("GOCACHE"), snapshot.Value("GOMODCACHE"))
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
			file := put(tc.path)
			c := linkerConfig(t, `packages:"./..." input_files_from_environment:"MANIFEST"`)
			c.SetEnvironment([]string{"MANIFEST=" + file})
			n, err := NormalizeInvocation(t.Context(), dir, goInvocation("test", c))
			if tc.refuse {
				if err == nil || n != nil || !strings.Contains(err.Error(), "MANIFEST") || !strings.Contains(err.Error(), file) || !strings.Contains(err.Error(), "guard-covered") {
					t.Fatalf("cache file must refuse by native classification: returned invocation=%t err=%v", n != nil, err)
				}
			} else if err != nil {
				t.Fatalf("native-observable exception refused: %v", err)
			}
		})
	}
	// Explicit invocation overrides supersede the persisted root; a parent
	// os.Getenv-only check would misjudge one of these two cases.
	file := put(filepath.Join(cache, "another-manifest.json"))
	other := filepath.Join(out, "other-cache")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	c := linkerConfig(t, `packages:"./..." input_files_from_environment:"MANIFEST"`)
	c.SetEnvironment([]string{"MANIFEST=" + file, "GOCACHE=" + other})
	if _, err := NormalizeInvocation(t.Context(), dir, goInvocation("test", c)); err != nil {
		t.Fatalf("effective override was ignored: %v", err)
	}
	// The same directory is excluded by another effective root despite its
	// exemption under the module-cache owner.
	file = put(filepath.Join(modules, "cache", "nested-build", "manifest.json"))
	c.SetEnvironment([]string{"MANIFEST=" + file, "GOCACHE=" + filepath.Dir(file)})
	if _, err := NormalizeInvocation(t.Context(), dir, goInvocation("test", c)); err == nil || !strings.Contains(err.Error(), "guard-covered") {
		t.Fatalf("overlapping effective root admitted: %v", err)
	}
	for _, tc := range []struct{ name, path, cause string }{
		{"toolchain-file", filepath.Join(snapshot.Value("GOROOT"), "VERSION"), "guard-covered"},
		{"machine-projection", "/proc/cpuinfo", "projection"},
		{"volatile-region", "/proc/stat", "volatile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name != "toolchain-file" && runtime.GOOS != "linux" {
				t.Skip("Linux classifier surface")
			}
			cfg := linkerConfig(t, `packages:"./..." input_files_from_environment:"MANIFEST"`)
			cfg.SetEnvironment([]string{"MANIFEST=" + tc.path})
			if _, err := NormalizeInvocation(t.Context(), dir, goInvocation("test", cfg)); err == nil || !strings.Contains(err.Error(), tc.cause) {
				t.Fatalf("%s admitted or lost its native cause: %v", tc.name, err)
			}
		})
	}
}
