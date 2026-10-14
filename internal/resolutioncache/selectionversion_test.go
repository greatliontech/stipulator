package resolutioncache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestIncompleteSelectionRecordsNeverServe keeps a version-one default
// record from lending its classification to the complete selection set.
func TestIncompleteSelectionRecordsNeverServe(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	current := Record{Selection: "default", Symbol: "example.com/p.F", Fingerprint: fingerprint("a"), Resolution: "resolved"}
	if err := InstallAll(dir, []Record{current}); err != nil {
		t.Fatal(err)
	}
	store, err := StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(store, mustName(t, current)))
	if err != nil {
		t.Fatal(err)
	}
	var written struct{ Version int }
	if err := json.Unmarshal(data, &written); err != nil || written.Version != 2 {
		t.Fatalf("writer version = %d, %v; want complete-selection version 2", written.Version, err)
	}
	legacy := current
	legacy.Fingerprint = fingerprint("e")
	data, err = json.Marshal(entry{Version: 1, Selection: legacy.Selection, Symbol: legacy.Symbol, Fingerprint: legacy.Fingerprint, Resolution: legacy.Resolution})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, mustName(t, legacy)), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); len(got) != 1 || got[0] != current {
		t.Fatalf("incomplete-selection record served beside current record: %+v", got)
	}
	if removed, kept, err := GC(t.Context(), dir, func(string, string) bool { return true }); err != nil || removed != 1 || kept != 1 {
		t.Fatalf("collecting incomplete selection records: removed=%d kept=%d err=%v", removed, kept, err)
	}
}
