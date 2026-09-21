package resolutioncache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func fingerprint(closure string) witnesscache.Fingerprint {
	return witnesscache.Fingerprint{MaximalClosure: strings.Repeat(closure, 32), TestVariantClosure: strings.Repeat("b", 32), Guards: guard.Guards{Toolchain: "go1.27.0", BuildConfig: strings.Repeat("c", 32)}, ResultKind: gofresh.CodeResult}
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
	if err := InstallAll(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	got := Load(dir)
	if len(got) != 1 || got[0] != rec {
		t.Fatalf("loaded %+v, want %+v", got, rec)
	}
	store, _ := StoreDir(dir)
	if entries, _ := os.ReadDir(store); len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), recordstore.Digest("race", "example.com/p.F")+"-") {
		t.Fatalf("store holds %v, want one file named by the identity digest", entries)
	}
	later := rec
	later.Fingerprint = fingerprint("e")
	later.Shape = strings.Repeat("f", 64)
	if err := InstallAll(dir, []Record{later}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); len(got) != 1 || got[0].Shape != later.Shape {
		t.Fatalf("after a superseding install: %+v, want the later record alone", got)
	}
	// Another selection is another identity.
	other := rec
	other.Selection = "default"
	if err := InstallAll(dir, []Record{other}); err != nil {
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
	asserted := good
	asserted.Fingerprint.ObservationAssertion = "caller assertion"
	proved := good
	proved.Fingerprint.ObservationProof = gofresh.ObservationProof{Strategy: gofresh.ObservationRTA, Subject: gofresh.Subject{Package: "example.com/p", Symbol: "F"}, Observable: true, Evidence: strings.Repeat("e", 32)}
	pure := good
	pure.Fingerprint.PurityAssertion = "source directive"
	unresolved := good
	unresolved.Resolution = "not_found"
	benchmark := good
	benchmark.Fingerprint.ResultKind = gofresh.Measurement
	noCompartment := good
	noCompartment.Fingerprint.TestVariantClosure = ""
	for name, rec := range map[string]Record{"runtime tier": observed, "observation assertion": asserted, "observation proof": proved, "purity tier": pure, "unresolved": unresolved, "no selection": {Symbol: "x", Fingerprint: fingerprint("a"), Resolution: "resolved"}, "benchmark result kind": benchmark, "no compartment digest": noCompartment} {
		if err := InstallAll(dir, []Record{rec}); err == nil {
			t.Fatalf("%s: installed", name)
		}
	}
	if err := InstallAll(dir, []Record{good}); err != nil {
		t.Fatal(err)
	}
	store, _ := StoreDir(dir)
	// Each planted file is named by the record it carries — its
	// identity and its own (distinct) fingerprint — so the file passes
	// the name-content check and the refusal is the ladder's.
	raw := func(v any) json.RawMessage {
		data, _ := json.Marshal(v)
		return data
	}
	// The fingerprint member stays typed: Gofresh's decoder refuses any
	// encoding of it but the form's own, so a map round trip (sorted
	// keys) would be refused for the form, never for the ladder's reason.
	write := func(closure string, mutate func(map[string]json.RawMessage, *witnesscache.Fingerprint)) {
		t.Helper()
		e := map[string]json.RawMessage{}
		data, _ := os.ReadFile(filepath.Join(store, mustName(t, good)))
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatal(err)
		}
		var planted witnesscache.Fingerprint
		if err := json.Unmarshal(e["fingerprint"], &planted); err != nil {
			t.Fatal(err)
		}
		planted.MaximalClosure = strings.Repeat(closure, 32)
		mutate(e, &planted)
		e["fingerprint"] = raw(planted)
		name := mustName(t, Record{Selection: good.Selection, Symbol: good.Symbol, Fingerprint: planted})
		out, _ := json.Marshal(e)
		if err := os.WriteFile(filepath.Join(store, name), out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("1", func(e map[string]json.RawMessage, _ *witnesscache.Fingerprint) { e["version"] = raw(version - 1) })
	write("2", func(e map[string]json.RawMessage, _ *witnesscache.Fingerprint) { e["resolvedBy"] = raw("someone") })
	write("3", func(e map[string]json.RawMessage, _ *witnesscache.Fingerprint) { e["resolution"] = raw("not_found") })
	write("4", func(_ map[string]json.RawMessage, fp *witnesscache.Fingerprint) {
		fp.RuntimeInputs = strings.Repeat("9", 32)
	})
	write("5", func(e map[string]json.RawMessage, _ *witnesscache.Fingerprint) { e["selection"] = raw("") })
	if got := Load(dir); len(got) != 1 || got[0] != good {
		t.Fatalf("loaded %+v, want the one good record", got)
	}
}

// A record whose file name disagrees with the record inside is ignored
// — the store serves nothing it did not name — and an install temporary
// beside the records is never read and never swept: a concurrent
// installer's rename is about to claim it.
//
//gofresh:pure
func TestMisnamedRecordsAndTemporariesAreNotRecords(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	rec := Record{Selection: "race", Symbol: "example.com/p.F", Fingerprint: fingerprint("a"), Resolution: "resolved", Shape: "func", Package: "example.com/p"}
	if err := InstallAll(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	store, _ := StoreDir(dir)
	sameIdentity := rec
	sameIdentity.Fingerprint = fingerprint("e")
	if err := os.Rename(filepath.Join(store, mustName(t, rec)), filepath.Join(store, mustName(t, sameIdentity))); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); len(got) != 0 {
		t.Fatalf("a record under another fingerprint's name served: %+v", got)
	}
	// Live by identity and still collected: nothing serves it.
	if removed, kept, err := GC(dir, func(string, string) bool { return true }); err != nil || removed != 1 || kept != 0 {
		t.Fatalf("gc of the fingerprint-misnamed record = %d removed, %d kept, %v", removed, kept, err)
	}
	if err := InstallAll(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(store, mustName(t, rec)), filepath.Join(store, mustName(t, sameIdentity))); err != nil {
		t.Fatal(err)
	}
	other := Record{Selection: "plain", Symbol: "example.com/p.G", Fingerprint: fingerprint("a")}
	if err := os.Rename(filepath.Join(store, mustName(t, sameIdentity)), filepath.Join(store, mustName(t, other))); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); len(got) != 0 {
		t.Fatalf("a record under another identity's name served: %+v", got)
	}
	temp := filepath.Join(store, ".resolutions-live.json")
	if err := os.WriteFile(temp, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The misnamed record is live by identity and still goes: nothing
	// serves it.
	removed, kept, err := GC(dir, func(string, string) bool { return true })
	if err != nil || removed != 1 || kept != 0 {
		t.Fatalf("gc = %d removed, %d kept, %v; want the misnamed record alone removed", removed, kept, err)
	}
	if _, err := os.Stat(temp); err != nil {
		t.Fatalf("the temporary was swept: %v", err)
	}
}

// mustName is the record's file name, or the test's failure — every
// record the tests name encodes.
func mustName(t *testing.T, rec Record) string {
	t.Helper()
	name, err := fileName(rec)
	if err != nil {
		t.Fatal(err)
	}
	return name
}
