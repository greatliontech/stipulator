package witnesscache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestLoadUnreadableIsEmpty pins the unreadable-record leg of
// REQ-evidence-witness-freshness: a corrupt or version-mismatched cache is
// an empty cache, so every test runs — a broken cache costs work, never
// correctness.
func TestLoadUnreadableIsEmpty(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	dir := t.TempDir()
	full := filepath.Join(dir, filepath.FromSlash(Path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := Load(dir); got != nil {
		t.Fatalf("absent cache loaded %d records", len(got))
	}

	if err := os.WriteFile(full, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("corrupt cache loaded %d records", len(got))
	}

	rec := Record{Package: "p", Test: "T", Outcomes: map[string]string{"p.T": "passed"}}
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	got := Load(dir)
	if len(got) != 1 || got[0].Key() != "p.T" {
		t.Fatalf("round trip lost the record: %+v", got)
	}

	// A version bump reads as empty — future formats never half-parse.
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	bumped := strings.Replace(string(data), `"version": 1`, `"version": 2`, 1)
	if bumped == string(data) {
		t.Fatal("version field not found to bump")
	}
	if err := os.WriteFile(full, []byte(bumped), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("version-mismatched cache loaded %d records", len(got))
	}
}
