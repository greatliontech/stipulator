package resolutioncache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestIncompleteSelectionRecordsNeverServe keeps prior single-view or
// source-certificate records from lending a classification missing multi-view
// SourceResult premises to a current view.
func TestIncompleteSelectionRecordsNeverServe(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	current := Record{
		Selection:    "default",
		Symbol:       "example.com/p.F",
		Views:        views("default", "a"),
		Resolution:   "resolved",
		Shape:        "shape",
		Package:      "example.com/p",
		WitnessClass: "example",
	}
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
	if err := json.Unmarshal(data, &written); err != nil || written.Version != 7 {
		t.Fatalf("writer version = %d, %v; want multi-view SourceResult version 7", written.Version, err)
	}
	for _, v := range []int{1, 2, 3, 4, 5, 6} {
		legacy := current.Clone()
		legacy.Views = views("default", string(rune('0'+v)))
		data, err = json.Marshal(entry{
			Version:      v,
			Selection:    legacy.Selection,
			Symbol:       legacy.Symbol,
			Views:        legacy.Views,
			Resolution:   legacy.Resolution,
			Shape:        legacy.Shape,
			Package:      legacy.Package,
			WitnessClass: legacy.WitnessClass,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store, mustName(t, legacy)), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := Load(dir); len(got) != 1 || !got[0].Equal(current) {
		t.Fatalf("incomplete-selection record served beside current record: %+v", got)
	}
	if removed, kept, err := GC(t.Context(), dir, func(string, string) bool { return true }); err != nil || removed != 6 || kept != 1 {
		t.Fatalf("collecting incomplete selection records: removed=%d kept=%d err=%v", removed, kept, err)
	}
}
