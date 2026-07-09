package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTargetsCmdOut pins the CLI verb's --out arm over this repository's own
// corpus (REQ-harden-export): the export lands at the path with a summary
// line, and a selection matching nothing refuses rather than emitting an
// empty document.
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
	c.SetArgs([]string{"--req", "REQ-harden-vacuity", "--out", out})
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
	if !strings.Contains(string(data), `"stipulatorTargets": 1`) || !strings.Contains(string(data), "Vacuous") {
		t.Fatalf("export = %s", data)
	}

	c2 := targetsCmd()
	c2.SetOut(&bytes.Buffer{})
	c2.SetErr(&bytes.Buffer{})
	c2.SetArgs([]string{"--req", "REQ-no-such"})
	if err := c2.Execute(); err == nil || !strings.Contains(err.Error(), "no targets") {
		t.Fatalf("empty selection exported: %v", err)
	}
}
