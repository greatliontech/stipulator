package resolutioncache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestIncompleteSelectionRecordsNeverServe keeps a historical default
// record from lending a classification missing selection or physical-source
// premises to a current view.
func TestIncompleteSelectionRecordsNeverServe(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	current := Record{Selection: "default", Symbol: "example.com/p.F", Fingerprint: fingerprint("a"), Certificate: certificate(), Resolution: "resolved", Shape: "shape", Package: "example.com/p", WitnessClass: "example"}
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
	if err := json.Unmarshal(data, &written); err != nil || written.Version != 6 {
		t.Fatalf("writer version = %d, %v; want source-certificate version 6", written.Version, err)
	}
	for _, v := range []int{1, 2, 3, 4, 5} {
		legacy := current
		legacy.Fingerprint = fingerprint(string(rune('0' + v)))
		data, err = json.Marshal(entry{Version: v, Selection: legacy.Selection, Symbol: legacy.Symbol, Fingerprint: legacy.Fingerprint, Certificate: legacy.Certificate, Resolution: legacy.Resolution, Shape: legacy.Shape, Package: legacy.Package, WitnessClass: legacy.WitnessClass})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store, mustName(t, legacy)), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := Load(dir); len(got) != 1 || got[0] != current {
		t.Fatalf("incomplete-selection record served beside current record: %+v", got)
	}
	if removed, kept, err := GC(t.Context(), dir, func(string, string) bool { return true }); err != nil || removed != 5 || kept != 1 {
		t.Fatalf("collecting incomplete selection records: removed=%d kept=%d err=%v", removed, kept, err)
	}
}
