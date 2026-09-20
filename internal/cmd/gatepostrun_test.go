package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// A problem the witness run itself finds — a registration no tests- or
// proves-role binding backs, the records clean — refuses the CLI gate's
// coverage judgment through the one rendering, exactly as the hygiene
// half does (REQ-check-preparation).
func TestGateRefusesAPostRunProblemOnTheCLI(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	if testing.Short() {
		t.Skip("resolves a fixture module")
	}
	neutralAmbient(t)
	dir := t.TempDir()
	// The witness run is the one seam: it reports a registration for
	// REQ-fix-b, which has an implements binding and no tests-role one —
	// the post-run problem — without spawning a test process.
	prior := runWitnessesPolicy
	runWitnessesPolicy = func(context.Context, *golang.Capture, verify.WitnessSeeding) (*verify.TestRun, error) {
		return &verify.TestRun{Registrations: []verify.Registration{{Package: "example.com/verifyfix/ok", Test: "TestDouble", Requirement: "REQ-fix-b"}}}, nil
	}
	t.Cleanup(func() { runWitnessesPolicy = prior })
	for path, content := range map[string]string{
		"go.mod":                         "module example.com/verifyfix\n\ngo 1.26.4\n",
		"ok/ok.go":                       "package ok\n\nfunc Double(x int) int { return 2 * x }\n\nfunc Round(x int) int { return x }\n",
		"ok/ok_test.go":                  "package ok\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif Double(2) != 4 {\n\t\tt.Fatal(\"broken\")\n\t}\n}\n",
		"specs/check.md":                 "# Check\n\n**REQ-fix-a** (behavior): The fixture MUST double.\n\n**REQ-fix-b** (behavior): The fixture MUST round.\n",
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		".stipulator/policy.textproto":   "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
		".stipulator/bindings/fix.textproto": "bindings {\n  requirement_id: \"REQ-fix-a\"\n  backend: \"go\"\n  symbol: \"example.com/verifyfix/ok.TestDouble\"\n  role: BINDING_ROLE_TESTS\n}\n" +
			"bindings {\n  requirement_id: \"REQ-fix-b\"\n  backend: \"go\"\n  symbol: \"example.com/verifyfix/ok.Round\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n",
	} {
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
	cmd := gateCmd()
	cmd.SetArgs([]string{})
	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "fix verification problems first (1)") {
		t.Fatalf("gate over a post-run problem = %v; want the one refusal naming the count", err)
	}
}
