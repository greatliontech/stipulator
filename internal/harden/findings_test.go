package harden

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestLoadFindings pins the document reading (REQ-harden-findings): a missing
// document is nothing measured, an unknown version is refused, unknown fields
// are ignored, and the pins arrive intact.
func TestLoadFindings(t *testing.T) {
	if got, err := LoadFindings(fstest.MapFS{}, FindingsPath); err != nil || got != nil {
		t.Fatalf("missing document: %v %v", got, err)
	}
	fsys := fstest.MapFS{FindingsPath: &fstest.MapFile{Data: []byte(`{"version": 1, "findings": [
		{"symbol": "p.F", "labels": ["REQ-x"], "bodyHash": "h",
		 "oracle": [{"symbol": "p.TestF", "hash": "wh"}],
		 "operatorSet": "go/2", "toolchain": "tc", "mutants": 2, "killed": 1,
		 "survivors": [{"position": "f.go:1:1", "operator": "zero return"}],
		 "futureField": true}
	]}`)}}
	fs2, err := LoadFindings(fsys, FindingsPath)
	if err != nil || len(fs2) != 1 {
		t.Fatalf("load: %v %v", fs2, err)
	}
	f := fs2[0]
	if f.Symbol != "p.F" || f.Labels[0] != "REQ-x" || f.Oracle[0].Hash != "wh" || f.Toolchain != "tc" || len(f.Survivors) != 1 {
		t.Fatalf("finding = %+v", f)
	}
	bad := fstest.MapFS{FindingsPath: &fstest.MapFile{Data: []byte(`{"version": 9, "findings": []}`)}}
	if _, err := LoadFindings(bad, FindingsPath); err == nil || !strings.Contains(err.Error(), "not understood") {
		t.Fatalf("unknown version accepted: %v", err)
	}
}
