package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	surfacewire "github.com/greatliontech/stipulator/bindingsurface"
)

// TestTargetsCmdOut pins the CLI verb's --out arm over this repository's own
// corpus: the export lands atomically as module-valid ProtoJSON, inline output
// has no summary, and a selection matching nothing preserves the destination.
//
//gofresh:pure
func TestTargetsCmdOut(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the corpus")
	}
	old := chdir
	chdir = "../.."
	defer func() { chdir = old }()

	out := filepath.Join(t.TempDir(), "targets.json")
	c := targetsCmd()
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.SetArgs([]string{"--req", "REQ-profile-root", "--out", out})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "wrote") {
		t.Fatalf("summary = %q", buf.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("file export lacks final newline: %q", data)
	}
	report, err := surfacewire.ParseJSON(data)
	if err != nil || len(report.GetSurfaces()) == 0 {
		t.Fatalf("export = %s, %v", data, err)
	}
	for _, surface := range report.GetSurfaces() {
		if !slices.Contains(surface.GetRequirementIds(), "REQ-profile-root") {
			t.Fatalf("filter admitted surface %v", surface)
		}
	}

	c2 := targetsCmd()
	c2.SetOut(&bytes.Buffer{})
	c2.SetErr(&bytes.Buffer{})
	c2.SetArgs([]string{"--backend", "no-such-backend", "--out", out})
	if err := c2.Execute(); err == nil || !strings.Contains(err.Error(), "no binding surfaces") {
		t.Fatalf("empty selection exported: %v", err)
	}
	unchanged, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(unchanged, data) {
		t.Fatalf("failed export changed destination: %v\n%s", err, unchanged)
	}

	c3 := targetsCmd()
	var inline bytes.Buffer
	c3.SetOut(&inline)
	c3.SetArgs([]string{"--req", "REQ-profile-root", "--backend", "go"})
	if err := c3.Execute(); err != nil {
		t.Fatal(err)
	}
	if inline.Len() == 0 || inline.Bytes()[inline.Len()-1] != '\n' {
		t.Fatalf("inline export lacks final newline: %q", inline.Bytes())
	}
	if _, err := surfacewire.ParseJSON(inline.Bytes()); err != nil {
		t.Fatalf("inline output is not only ProtoJSON: %v\n%s", err, inline.Bytes())
	}

	c4 := targetsCmd()
	c4.SetOut(&bytes.Buffer{})
	c4.SetErr(&bytes.Buffer{})
	c4.SetArgs([]string{"--staged-diff"})
	if err := c4.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("retired non-ProtoJSON targets arm remains callable: %v", err)
	}
}

func TestWriteAtomicFileReplacesWithoutResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicFile(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("replacement = %q, %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("replacement mode = %v, %v", info, err)
	}
	if os.SameFile(before, info) {
		t.Fatal("replacement rewrote the destination inode instead of renaming a complete file")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "targets.json" {
		t.Fatalf("atomic write residue = %v, %v", entries, err)
	}
}
