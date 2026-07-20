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

// The gap record's whole CLI lifecycle in one tree: bulk self-landed
// declaration, firing a manual condition, retraction, and the dangling
// bulk repair — prune --dangling judging from corpus and records alone,
// with its check form deleting nothing.
//
// Deliberately not //gofresh:pure: builds and executes the CLI binary.
func TestGapLifecycleCLI(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-bulk", "REQ-gap-retract", "REQ-gap-prune-dangling")
	if testing.Short() {
		t.Skip("builds the CLI")
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
	write(".stipulator/manifest.textproto", "include: \"specs/**/*.md\"\n")
	write("specs/s.md", "# S\n\n**REQ-gl-a** (behavior): It MUST a.\n\n**REQ-gl-b** (behavior): It MUST b.\n")

	run := func(wantExit int, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "NO_COLOR=1")
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
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// Bulk self-landed declaration: each requirement lands on itself.
	run(0, "gap", "--req", "REQ-gl-a", "--req", "REQ-gl-b", "--reason", "spec ahead of code", "--covered", "self")
	if c := read(".stipulator/gaps/gl-a.textproto"); !strings.Contains(c, `covered: "REQ-gl-a"`) {
		t.Fatalf("self sentinel did not land on the requirement itself:\n%s", c)
	}
	if c := read(".stipulator/gaps/gl-b.textproto"); !strings.Contains(c, `covered: "REQ-gl-b"`) {
		t.Fatalf("self sentinel shared the first requirement:\n%s", c)
	}

	// Re-declare one as manual, fire it, then retract it.
	run(0, "gap", "--req", "REQ-gl-a", "--reason", "external judgment", "--manual", "ops signed off")
	run(0, "gap", "--req", "REQ-gl-a", "--fired")
	if c := read(".stipulator/gaps/gl-a.textproto"); !strings.Contains(c, "fired: true") {
		t.Fatalf("firing left the record unfired:\n%s", c)
	}
	run(0, "gap", "--req", "REQ-gl-a", "--retract")
	if _, err := os.Stat(filepath.Join(dir, ".stipulator/gaps/gl-a.textproto")); !os.IsNotExist(err) {
		t.Fatal("retraction left the record behind")
	}

	// Orphan the remaining gap by dropping its requirement, then repair:
	// check reports without deleting, the repair deletes, check goes clean.
	write("specs/s.md", "# S\n\n**REQ-gl-a** (behavior): It MUST a.\n")
	out := run(2, "prune", "--dangling", "--check")
	if !strings.Contains(out, "gl-b.textproto") {
		t.Fatalf("check did not name the dangling record:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".stipulator/gaps/gl-b.textproto")); err != nil {
		t.Fatal("check deleted the record — must be dry-run")
	}
	run(0, "prune", "--dangling")
	if _, err := os.Stat(filepath.Join(dir, ".stipulator/gaps/gl-b.textproto")); !os.IsNotExist(err) {
		t.Fatal("dangling repair left the record behind")
	}
	run(0, "prune", "--dangling", "--check")
}
