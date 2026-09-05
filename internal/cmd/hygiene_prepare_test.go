package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
	"github.com/spf13/cobra"
)

// TestCommandsRefuseHygieneBeforeAnyWitness pins the record-hygiene leg
// on the CLI: gate, verify, and prune fail on a dangling binding before
// their witness run — the fixture's test writes a marker when it
// executes, and none of them leaves one (REQ-check-preparation) —
// while gap --list, a read surface, still answers.
func TestCommandsRefuseHygieneBeforeAnyWitness(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	if testing.Short() {
		t.Skip("prepares a race policy over a fixture tree")
	}
	neutralAmbient(t)
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "executed")
	files := map[string]string{
		"go.mod":                               "module example.com/hygfix\n\ngo 1.26.4\n",
		"ok/ok.go":                             "package ok\n\nfunc Double(x int) int { return 2 * x }\n",
		"ok/ok_test.go":                        "package ok\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestDouble(t *testing.T) {\n\t_ = os.WriteFile(" + strconv.Quote(marker) + ", []byte(\"ran\"), 0o644)\n\tDouble(2)\n}\n",
		"specs/check.md":                       "# Check\n\n**REQ-fix-may** (behavior): The fixture MAY pass.\n",
		".stipulator/manifest.textproto":       "include: \"specs/**/*.md\"\n",
		".stipulator/policy.textproto":         "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
		".stipulator/bindings/ghost.textproto": "bindings {\n  requirement_id: \"REQ-fix-ghost\"\n  backend: \"go\"\n  symbol: \"example.com/hygfix/ok.Double\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n",
		".stipulator/gaps/deferred.textproto":  "requirement_id: \"REQ-fix-may\"\nreason: \"deferred\"\nlands { manual { condition: \"later\" } }\n",
	}
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	priorDir := chdir
	chdir = dir
	t.Cleanup(func() { chdir = priorDir })
	executed := func() bool { _, err := os.Stat(marker); return err == nil }
	// Every argument list is non-nil: cobra reads the process arguments
	// for a nil list, and the test binary's own flags ride there — under
	// a mutation oracle the rapid pinning flags, which no command
	// accepts.
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
		args []string
	}{{"gate", gateCmd(), []string{"--quiet"}}, {"verify", verifyCmd(), []string{}}, {"prune", pruneCmd(), []string{"--check"}}} {
		tc.cmd.SetArgs(tc.args)
		err := tc.cmd.ExecuteContext(context.Background())
		if err == nil || !strings.Contains(err.Error(), "fix verification problems first") {
			t.Fatalf("%s: err = %v, want the hygiene refusal", tc.name, err)
		}
		if executed() {
			t.Fatalf("%s: a witness executed under records that fail hygiene; the refusal fires before any child", tc.name)
		}
	}
	// Positive control: with the ghost binding gone, gate executes.
	if err := os.Remove(filepath.Join(dir, ".stipulator/bindings/ghost.textproto")); err != nil {
		t.Fatal(err)
	}
	gate := gateCmd()
	gate.SetArgs([]string{"--quiet"})
	if err := gate.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !executed() {
		t.Fatal("the clean tree executed no witness; the no-witness oracle above proves nothing")
	}
	// gap --list is a read surface: it answers under the dirty records
	// too, warning rather than refusing (REQ-gap-list).
	if err := os.WriteFile(filepath.Join(dir, ".stipulator/bindings/ghost.textproto"), []byte(files[".stipulator/bindings/ghost.textproto"]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gapListRun(context.Background()); err != nil {
		t.Fatalf("gap --list: %v", err)
	}
}

// neutralAmbient pins the ambient controls policy normalization reads to
// a known hermetic state, so host configuration cannot steer these tests.
func neutralAmbient(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("GOENV", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOPACKAGESDRIVER", "")
	t.Setenv("GOTOOLCHAIN", "local")
}

// TestNoTestVerificationNeedsNoPolicyRecord pins that only a witness run
// consumes the accepted policy: verify --no-test and prune --no-test
// resolve their bindings on a tree that has no policy record at all
// (REQ-policy-explicit binds witness execution, not resolution).
func TestNoTestVerificationNeedsNoPolicyRecord(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("resolves a fixture module through the verification backend")
	}
	neutralAmbient(t)
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                           "module example.com/notest\n\ngo 1.26.4\n",
		"ok/ok.go":                         "package ok\n\nfunc Double(x int) int { return 2 * x }\n",
		"specs/check.md":                   "# Check\n\n**REQ-fix-may** (behavior): The fixture MAY pass.\n",
		".stipulator/manifest.textproto":   "include: \"specs/**/*.md\"\n",
		".stipulator/bindings/b.textproto": "bindings {\n  requirement_id: \"REQ-fix-may\"\n  backend: \"go\"\n  symbol: \"example.com/notest/ok.Double\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n",
	}
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	priorDir := chdir
	chdir = dir
	t.Cleanup(func() { chdir = priorDir })
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
		args []string
	}{{"verify", verifyCmd(), []string{"--no-test"}}, {"prune", pruneCmd(), []string{"--no-test", "--check"}}} {
		tc.cmd.SetArgs(tc.args)
		if err := tc.cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%s without a policy record: %v", tc.name, err)
		}
	}
}
