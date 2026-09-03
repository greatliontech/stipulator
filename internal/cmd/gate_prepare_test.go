package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestGateVocabularyRefusesBeforeAnyWitness pins the CLI gate's parse
// leg: an unknown view, bucket, or requirement identifier refuses
// before the witness run — the fixture's one test writes a marker when
// it executes, and no refused invocation leaves one; the valid
// invocation does (REQ-check-preparation). Runs the command in process
// so a mutant of the command is what executes.
func TestGateVocabularyRefusesBeforeAnyWitness(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	if testing.Short() {
		t.Skip("executes a race-instrumented policy over a fixture tree")
	}
	neutralAmbient(t)
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "executed")
	files := map[string]string{
		"go.mod":                         "module example.com/gatefix\n\ngo 1.26.4\n",
		"ok/ok.go":                       "package ok\n\nfunc Double(x int) int { return 2 * x }\n",
		"ok/ok_test.go":                  "package ok\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestDouble(t *testing.T) {\n\t_ = os.WriteFile(" + strconv.Quote(marker) + ", []byte(\"ran\"), 0o644)\n\tDouble(2)\n}\n",
		"specs/check.md":                 "# Check\n\n**REQ-fix-may** (behavior): The fixture MAY pass.\n",
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		".stipulator/policy.textproto":   "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
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
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--json", "--view", "bogus"}, "unknown view"},
		{[]string{"--bucket", "bogus"}, "unknown bucket"},
		{[]string{"--req", "REQ-fix-nope"}, "REQ-fix-nope"},
	} {
		cmd := gateCmd()
		cmd.SetArgs(tc.args)
		err := cmd.ExecuteContext(context.Background())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: err = %v, want a refusal naming %q", tc.args, err, tc.want)
		}
		if executed() {
			t.Fatalf("%v: a witness executed under a refused vocabulary; the refusal fires before any child", tc.args)
		}
	}
	// Positive control: the valid invocation executes the witness.
	cmd := gateCmd()
	cmd.SetArgs([]string{"--quiet"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !executed() {
		t.Fatal("the valid gate executed no witness; the no-witness oracle above proves nothing")
	}
}
