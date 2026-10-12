package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestVersionWorksWithoutACorpus(t *testing.T) {
	stipulate.Covers(t, "REQ-cli-version")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("malformed module\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("PATH", t.TempDir())
	var first string
	for _, args := range [][]string{{"version"}, {"--version"}, {"-C", "missing", "version"}, {"-C", "missing", "--version"}} {
		root := newRootCmd()
		root.SetArgs(args)
		var out, stderr bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&stderr)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		got := out.String()
		if !strings.HasPrefix(got, "stipulator ") || strings.Count(got, "\n") != 1 || stderr.Len() != 0 {
			t.Fatalf("%v: %q / %q", args, got, stderr.String())
		}
		for _, field := range []string{"revision=", "modified=", "go=", "gofresh="} {
			if !strings.Contains(got, field) {
				t.Fatalf("missing %s: %q", field, got)
			}
		}
		if first != "" && got != first {
			t.Fatalf("forms disagree: %q / %q", first, got)
		}
		first = got
		var status bytes.Buffer
		events := 0
		if err := execute(context.Background(), args, func(*stipulatorv1.ProgressEvent) { events++ }, &status); err != nil || events != 0 || status.Len() != 0 {
			t.Fatalf("query entered evaluation: err=%v events=%d status=%q", err, events, status.String())
		}
	}
	root := newRootCmd()
	root.SetArgs([]string{"version", "unexpected"})
	if err := root.Execute(); err == nil {
		t.Fatal("version accepted an operand")
	}
	if versionQuery(newRootCmd(), []string{"-C", "version", "compile"}) {
		t.Fatal("a directory operand bypassed the command")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("query wrote state: %v, %v", entries, err)
	}
}

func TestVersionExecutableNeedsNoGoOrGit(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	stipulate.Covers(t, "REQ-cli-version")
	bin := filepath.Join(t.TempDir(), "stipulator")
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "github.com/greatliontech/stipulator/cmd/stipulator").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"version"}, {"--version"}, {"--help"}} {
		cmd := exec.CommandContext(t.Context(), bin, args...)
		cmd.Dir = dir
		cmd.Env = gotool.SetEnv(os.Environ(), "PATH", "")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "version") && !strings.HasPrefix(string(out), "stipulator ") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("query wrote state: %v, %v", entries, err)
	}
}
