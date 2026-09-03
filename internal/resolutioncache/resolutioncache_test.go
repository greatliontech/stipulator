package resolutioncache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func fingerprint(closure string) witnesscache.Fingerprint {
	return witnesscache.Fingerprint{
		MaximalClosure: strings.Repeat(closure, 32), TestVariantClosure: strings.Repeat("b", 32),
		Toolchain: "go1.27.0", BuildConfig: strings.Repeat("c", 32), ResultKind: gofresh.CodeResult,
	}
}

// TestRecordsRoundTripOnePerIdentity pins the store's layout: a record
// installs under its selection and symbol beside the fingerprint's
// digest, loads back whole, and a later fingerprint for the same
// identity supersedes the earlier file — one record per identity.
func TestRecordsRoundTripOnePerIdentity(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	rec := Record{Selection: "race", Symbol: "example.com/p.F", Fingerprint: fingerprint("a"), Resolution: "resolved", Shape: strings.Repeat("d", 64), Package: "example.com/p", WitnessClass: "example", NeverServe: ""}
	if err := Install(dir, rec); err != nil {
		t.Fatal(err)
	}
	got := Load(dir)
	if len(got) != 1 || got[0] != rec {
		t.Fatalf("loaded %+v, want %+v", got, rec)
	}
	store, _ := StoreDir(dir)
	if entries, _ := os.ReadDir(store); len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), digest("race", "example.com/p.F")+"-") {
		t.Fatalf("store holds %v, want one file named by the identity digest", entries)
	}
	later := rec
	later.Fingerprint = fingerprint("e")
	later.Shape = strings.Repeat("f", 64)
	if err := Install(dir, later); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); len(got) != 1 || got[0].Shape != later.Shape {
		t.Fatalf("after a superseding install: %+v, want the later record alone", got)
	}
	// Another selection is another identity.
	other := rec
	other.Selection = "default"
	if err := Install(dir, other); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); len(got) != 2 {
		t.Fatalf("two selections loaded as %d records", len(got))
	}
	removed, kept, err := GC(dir, func(selection, symbol string) bool { return selection == "race" })
	if err != nil || removed != 1 || kept != 1 {
		t.Fatalf("gc removed %d kept %d err %v, want 1/1", removed, kept, err)
	}
}

// TestRecordsRefuseWhatTheStoreDoesNotServe pins the fail-closed reads:
// a prior version, an unknown field, an unresolved resolution, and a
// fingerprint carrying an observation, purity, or runtime tier are all
// ignored on load and refused on install.
func TestRecordsRefuseWhatTheStoreDoesNotServe(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	good := Record{Selection: "default", Symbol: "example.com/p.F", Fingerprint: fingerprint("a"), Resolution: "resolved"}
	observed := good
	observed.Fingerprint.RuntimeInputs = strings.Repeat("9", 32)
	pure := good
	pure.Fingerprint.PurityAssertion = "source directive"
	unresolved := good
	unresolved.Resolution = "not_found"
	benchmark := good
	benchmark.Fingerprint.ResultKind = gofresh.Measurement
	noCompartment := good
	noCompartment.Fingerprint.TestVariantClosure = ""
	for name, rec := range map[string]Record{"observation tier": observed, "purity tier": pure, "unresolved": unresolved, "no selection": {Symbol: "x", Fingerprint: fingerprint("a"), Resolution: "resolved"}, "benchmark result kind": benchmark, "no compartment digest": noCompartment} {
		if err := Install(dir, rec); err == nil {
			t.Fatalf("%s: installed", name)
		}
	}
	if err := Install(dir, good); err != nil {
		t.Fatal(err)
	}
	store, _ := StoreDir(dir)
	write := func(name string, mutate func(map[string]any)) {
		t.Helper()
		e := map[string]any{}
		data, _ := os.ReadFile(filepath.Join(store, fileName(good)))
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatal(err)
		}
		mutate(e)
		out, _ := json.Marshal(e)
		if err := os.WriteFile(filepath.Join(store, name), out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("prior-version.json", func(e map[string]any) { e["version"] = version - 1 })
	write("unknown-field.json", func(e map[string]any) { e["resolvedBy"] = "someone" })
	write("unresolved.json", func(e map[string]any) { e["resolution"] = "not_found" })
	write("runtime-tier.json", func(e map[string]any) {
		e["fingerprint"].(map[string]any)["runtimeInputs"] = strings.Repeat("9", 32)
	})
	if got := Load(dir); len(got) != 1 || got[0] != good {
		t.Fatalf("loaded %+v, want the one good record", got)
	}
}
