package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// A resolved-mode prune narrows witness evaluation to the gapped
// requirements' bound subjects — the ungapped requirement's witness
// never executes for pruning — deletes the resolved record, and with no
// gap records left performs deletion work only: no witness evaluation
// at all (REQ-gap-resolved-pruned).
//
// Deliberately not //gofresh:pure: builds and executes the CLI binary.
func TestPruneScopedWitnessEvaluationAndDeletionOnlyFastPath(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned")
	if testing.Short() {
		t.Skip("builds the CLI and executes a policy over a fixture tree")
	}
	bin := filepath.Join(t.TempDir(), "stipulator")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/greatliontech/stipulator/cmd/stipulator").CombinedOutput(); err != nil {
		t.Fatalf("building the CLI: %v\n%s", err, out)
	}
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/prunefix\n\ngo 1.26.4\n")
	write("ok/ok.go", "package ok\n\nfunc Double(x int) int { return 2 * x }\n")
	write("ok/ok_test.go", "package ok\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif Double(2) != 4 {\n\t\tt.Fatal(\"broken\")\n\t}\n}\n")
	write("tri/tri.go", "package tri\n\nfunc Triple(n int) int { return 3 * n }\n")
	write("tri/tri_test.go", "package tri\n\nimport \"testing\"\n\nfunc TestTriple(t *testing.T) {\n\tif Triple(2) != 6 {\n\t\tt.Fatal(\"broken\")\n\t}\n}\n")
	write(".stipulator/manifest.textproto", "include: \"specs/**/*.md\"\n")
	write(".stipulator/policy.textproto", "invocations {\n  name: \"race\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n")
	write("specs/p.md", "# P\n\n**REQ-pr-a** (behavior): The fixture MUST double.\n\n**REQ-pr-b** (behavior): The fixture MUST triple.\n")

	env := append(os.Environ(), "GOENV=off", "GOFLAGS=", "GOPACKAGESDRIVER=", "GOTOOLCHAIN=local", "NO_COLOR=1")
	run := func(wantExit int, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("running %v: %v\n%s", args, err, out.String())
		}
		if code != wantExit {
			t.Fatalf("%v exit = %d, want %d\n%s", args, code, wantExit, out.String())
		}
		return out.String()
	}

	run(0, "bind", "--req", "REQ-pr-a", "--symbol", "example.com/prunefix/ok.TestDouble", "--role", "tests")
	run(0, "bind", "--req", "REQ-pr-b", "--symbol", "example.com/prunefix/tri.TestTriple", "--role", "tests")
	run(0, "gap", "--req", "REQ-pr-a", "--reason", "external judgment", "--manual", "ops signed off")
	run(0, "gap", "--req", "REQ-pr-a", "--fired")

	// Cold tree: the scoped evaluation executes the gapped requirement's
	// witness and nothing else, resolves the gap, and deletes it.
	out := run(0, "prune")
	if !strings.Contains(out, "scoped to 1 gapped requirements") {
		t.Fatalf("prune did not name its scope:\n%s", out)
	}
	if !strings.Contains(out, "witnessed: 1 ran") {
		t.Fatalf("prune executed more than the gap-bound witness:\n%s", out)
	}
	if !strings.Contains(out, "1 resolved gaps pruned") {
		t.Fatalf("resolved gap not pruned:\n%s", out)
	}
	if !strings.Contains(out, "evaluated 1 gap records") {
		t.Fatalf("prune did not name the gap-record count:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".stipulator/gaps/pr-a.textproto")); !os.IsNotExist(err) {
		t.Fatal("resolved gap record left behind")
	}

	// A covered(<id>) landing condition pulls its target requirement
	// into the scope: the resolution judgment reads the target's
	// coverage, so its witness joins the evaluation.
	run(0, "gap", "--req", "REQ-pr-a", "--reason", "lands with the sibling", "--covered", "REQ-pr-b")

	// The list read surface shares the same scoped evaluation: the row
	// carries the declaration fields beside the evaluated state.
	out = run(0, "gap", "--list")
	if !strings.Contains(out, "scoped to 2 gapped requirements") {
		t.Fatalf("list did not scope to the gap-relevant requirements:\n%s", out)
	}
	if !strings.Contains(out, "resolved  REQ-pr-a  covered(REQ-pr-b)") {
		t.Fatalf("list row missing the evaluated state and condition:\n%s", out)
	}

	out = run(0, "prune")
	if !strings.Contains(out, "scoped to 2 gapped requirements") {
		t.Fatalf("condition target not folded into the scope:\n%s", out)
	}
	if !strings.Contains(out, "1 resolved gaps pruned") {
		t.Fatalf("covered-condition gap not pruned:\n%s", out)
	}

	// No gap records left: deletion work only, no witness evaluation.
	out = run(0, "prune")
	if !strings.Contains(out, "no gap records - nothing to evaluate") {
		t.Fatalf("gapless prune did not take the deletion-only fast path:\n%s", out)
	}
	if strings.Contains(out, "witnessing:") {
		t.Fatalf("gapless prune still gathered witness evidence:\n%s", out)
	}
	out = run(0, "prune", "--check")
	if !strings.Contains(out, "prune: clean") {
		t.Fatalf("gapless prune --check not clean:\n%s", out)
	}
}
