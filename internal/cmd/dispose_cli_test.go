package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// The CLI supersede runs on the corpus as edited — the source removed,
// the successor declaring — in one step, and takes --force for a source
// no record names, as the MCP form does (REQ-change-split-merge).
//
//gofresh:pure
func TestDisposeSupersedeCLIIsOneStep(t *testing.T) {
	stipulate.Covers(t, "REQ-change-split-merge")
	dir := t.TempDir()
	write := func(path, content string) {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".stipulator/manifest.textproto", "include: \"specs/**/*.md\"\n")
	// The corpus as edited: REQ-d-old is gone, REQ-d-new supersedes it,
	// and no record names the source.
	write("specs/a.md", "# T\n\n**REQ-d-new** (behavior, supersedes REQ-d-old): It MUST new.\n")
	priorDir := chdir
	chdir = dir
	t.Cleanup(func() { chdir = priorDir })
	run := func(args ...string) error {
		cmd := disposeCmd()
		cmd.SetArgs(append([]string{"supersede"}, args...))
		return cmd.ExecuteContext(context.Background())
	}
	if err := run("--from", "REQ-d-old", "--into", "REQ-d-new"); err == nil || !strings.Contains(err.Error(), "requires --force") {
		t.Fatalf("unrecorded source without --force: %v", err)
	}
	if err := run("--from", "REQ-d-old", "--into", "REQ-d-new", "--force"); err != nil {
		t.Fatalf("one-step supersede with --force: %v", err)
	}
	tomb, err := os.ReadFile(filepath.Join(dir, ".stipulator/tombstones.textproto"))
	if err != nil || !strings.Contains(string(tomb), "REQ-d-old") {
		t.Fatalf("tombstone: %v\n%s", err, tomb)
	}
	if _, err := mustCompile(dir); err != nil {
		t.Fatalf("the corpus does not compile after the disposition: %v", err)
	}
}
