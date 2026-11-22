package resolutioncache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
	"pgregory.net/rapid"
)

func fingerprint(closure string) witnesscache.Fingerprint {
	return witnesscache.Fingerprint{
		MaximalClosure:     strings.Repeat(closure, 32),
		TestVariantClosure: strings.Repeat("b", 32),
		ClosureStrategy:    gofresh.ClosureStrategy,
		ToolchainContent:   "toolchain-content",
		AdmissionPolicy:    "admission-policy",
		Guards:             gofresh.Guards{Toolchain: "go1.27.0", BuildConfig: strings.Repeat("c", 32)},
		ResultKind:         gofresh.SourceResult,
	}
}

func views(selection, closure string) []ViewEntry {
	return []ViewEntry{{Selection: selection, Present: true, Fingerprint: fingerprint(closure)}}
}

// TestRecordsRoundTripOnePerIdentity pins the store's layout: a record
// installs under its selection and symbol beside the views array's
// digest, loads back whole, and a later view sequence for the same
// identity supersedes the earlier file — one record per identity.
func TestRecordsRoundTripOnePerIdentity(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	rec := Record{
		Selection: "race",
		Symbol:    "example.com/p.F",
		Views: []ViewEntry{
			{Selection: "default", Present: false},
			{Selection: "race", Present: true, Fingerprint: fingerprint("a")},
		},
		Resolution:   "resolved",
		Shape:        strings.Repeat("d", 32),
		Package:      "example.com/p",
		WitnessClass: "example",
	}
	if err := InstallAll(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	got := Load(dir)
	if len(got) != 1 || !got[0].Equal(rec) {
		t.Fatalf("loaded %+v, want %+v", got, rec)
	}
	store, _ := StoreDir(dir)
	if entries, _ := os.ReadDir(store); len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), recordstore.Digest("race", "example.com/p.F")+"-") {
		t.Fatalf("store holds %v, want one file named by the identity digest", entries)
	}
	later := rec.Clone()
	later.Views[1].Fingerprint = fingerprint("e")
	later.Shape = strings.Repeat("f", 32)
	if err := InstallAll(dir, []Record{later}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); len(got) != 1 || !got[0].Equal(later) {
		t.Fatalf("after a superseding install: %+v, want the later record alone", got)
	}
	// Another selection is another identity.
	other := Record{
		Selection: "default",
		Symbol:    "example.com/p.F",
		Views: []ViewEntry{
			{Selection: "default", Present: true, Fingerprint: fingerprint("a")},
			{Selection: "race", Present: true, Fingerprint: fingerprint("b")},
		},
		Resolution:   "resolved",
		Shape:        strings.Repeat("d", 32),
		Package:      "example.com/p",
		WitnessClass: "example",
	}
	if err := InstallAll(dir, []Record{other}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); len(got) != 2 {
		t.Fatalf("two selections loaded as %d records", len(got))
	}
	removed, kept, err := GC(t.Context(), dir, func(selection, symbol string) bool { return selection == "race" })
	if err != nil || removed != 1 || kept != 1 {
		t.Fatalf("gc removed %d kept %d err %v, want 1/1", removed, kept, err)
	}
	nonCallable := Record{
		Selection:    "race",
		Symbol:       "example.com/p.T",
		Views:        views("race", "c"),
		Resolution:   "resolved",
		Shape:        strings.Repeat("c", 32),
		Package:      "example.com/p",
		WitnessClass: "example",
		NeverServe:   "not a runnable witness",
	}
	if err := InstallAll(dir, []Record{nonCallable}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); len(got) != 2 {
		t.Fatalf("after installing non-callable record: %+v, want 2 records", len(got))
	}
}

// TestRecordsRefuseWhatTheStoreDoesNotServe pins the fail-closed reads:
// a prior version, an unknown field, an unresolved resolution, invalid
// multi-view precedence, and a fingerprint carrying non-SourceResult tiers
// are all ignored on load and refused on install.
func TestRecordsRefuseWhatTheStoreDoesNotServe(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	good := Record{
		Selection:    "default",
		Symbol:       "example.com/p.F",
		Views:        views("default", "a"),
		Resolution:   "resolved",
		Shape:        "shape",
		Package:      "example.com/p",
		WitnessClass: "example",
	}
	withFP := func(mut func(*witnesscache.Fingerprint)) Record {
		r := good.Clone()
		mut(&r.Views[0].Fingerprint)
		return r
	}
	badCases := map[string]Record{
		"runtime tier":          withFP(func(f *witnesscache.Fingerprint) { f.RuntimeInputs = strings.Repeat("9", 32) }),
		"observation assertion": withFP(func(f *witnesscache.Fingerprint) { f.ObservationAssertion = "caller assertion" }),
		"observation proof": withFP(func(f *witnesscache.Fingerprint) {
			f.ObservationProof = gofresh.ObservationProof{Strategy: gofresh.ObservationRTA, Subject: gofresh.Subject{Package: "example.com/p", Symbol: "F"}, Observable: true, Evidence: strings.Repeat("e", 32)}
		}),
		"purity tier":           withFP(func(f *witnesscache.Fingerprint) { f.PurityAssertion = "source directive" }),
		"code result kind":      withFP(func(f *witnesscache.Fingerprint) { f.ResultKind = gofresh.CodeResult }),
		"benchmark result kind": withFP(func(f *witnesscache.Fingerprint) { f.ResultKind = gofresh.Measurement }),
		"no compartment digest": withFP(func(f *witnesscache.Fingerprint) { f.TestVariantClosure = "" }),
		"unresolved": func() Record {
			r := good.Clone()
			r.Resolution = "not_found"
			return r
		}(),
		"no selection": {Symbol: "example.com/p.F", Views: views("default", "a"), Resolution: "resolved", Shape: "shape", Package: "example.com/p", WitnessClass: "example"},
		"empty views": func() Record {
			r := good.Clone()
			r.Views = nil
			return r
		}(),
		"duplicate view selection": {
			Selection:    "default",
			Symbol:       "example.com/p.F",
			Views:        []ViewEntry{{Selection: "default", Present: true, Fingerprint: fingerprint("a")}, {Selection: "default", Present: false}},
			Resolution:   "resolved",
			Shape:        "shape",
			Package:      "example.com/p",
			WitnessClass: "example",
		},
		"present before resolving view": {
			Selection:    "race",
			Symbol:       "example.com/p.F",
			Views:        []ViewEntry{{Selection: "default", Present: true, Fingerprint: fingerprint("a")}, {Selection: "race", Present: true, Fingerprint: fingerprint("b")}},
			Resolution:   "resolved",
			Shape:        "shape",
			Package:      "example.com/p",
			WitnessClass: "example",
		},
		"resolving view absent": {
			Selection:    "default",
			Symbol:       "example.com/p.F",
			Views:        []ViewEntry{{Selection: "default", Present: false}},
			Resolution:   "resolved",
			Shape:        "shape",
			Package:      "example.com/p",
			WitnessClass: "example",
		},
		"resolving view missing from views": {
			Selection:    "race",
			Symbol:       "example.com/p.F",
			Views:        []ViewEntry{{Selection: "default", Present: false}},
			Resolution:   "resolved",
			Shape:        "shape",
			Package:      "example.com/p",
			WitnessClass: "example",
		},
		"absent view carrying fingerprint": {
			Selection:    "race",
			Symbol:       "example.com/p.F",
			Views:        []ViewEntry{{Selection: "default", Present: false, Fingerprint: fingerprint("a")}, {Selection: "race", Present: true, Fingerprint: fingerprint("b")}},
			Resolution:   "resolved",
			Shape:        "shape",
			Package:      "example.com/p",
			WitnessClass: "example",
		},
		"present view missing fingerprint": {
			Selection:    "default",
			Symbol:       "example.com/p.F",
			Views:        []ViewEntry{{Selection: "default", Present: true}},
			Resolution:   "resolved",
			Shape:        "shape",
			Package:      "example.com/p",
			WitnessClass: "example",
		},
	}
	for name, rec := range badCases {
		if err := InstallAll(dir, []Record{rec}); err == nil {
			t.Fatalf("%s: installed", name)
		}
	}
	if err := InstallAll(dir, []Record{good}); err != nil {
		t.Fatal(err)
	}
	store, _ := StoreDir(dir)
	raw := func(v any) json.RawMessage {
		data, _ := json.Marshal(v)
		return data
	}
	write := func(closure string, mutate func(map[string]json.RawMessage, *[]ViewEntry)) {
		t.Helper()
		e := map[string]json.RawMessage{}
		data, _ := os.ReadFile(filepath.Join(store, mustName(t, good)))
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatal(err)
		}
		planted := views("default", closure)
		mutate(e, &planted)
		e["views"] = raw(planted)
		sel := good.Selection
		if rawSel, ok := e["selection"]; ok {
			_ = json.Unmarshal(rawSel, &sel)
		}
		name := mustName(t, Record{Selection: sel, Symbol: good.Symbol, Views: planted})
		out, _ := json.Marshal(e)
		if err := os.WriteFile(filepath.Join(store, name), out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("1", func(e map[string]json.RawMessage, _ *[]ViewEntry) { e["version"] = raw(version - 1) })
	write("2", func(e map[string]json.RawMessage, _ *[]ViewEntry) { e["resolvedBy"] = raw("someone") })
	write("3", func(e map[string]json.RawMessage, _ *[]ViewEntry) { e["resolution"] = raw("not_found") })
	write("4", func(_ map[string]json.RawMessage, vs *[]ViewEntry) {
		(*vs)[0].Fingerprint.ResultKind = gofresh.CodeResult
		(*vs)[0].Fingerprint.DynamicStateStrategy = gofresh.DynamicStateStrategy
		(*vs)[0].Fingerprint.ObservationAssertion = "caller"
		(*vs)[0].Fingerprint.RuntimeInputs = strings.Repeat("9", 32)
		(*vs)[0].Fingerprint.RuntimeDigest = strings.Repeat("8", 32)
	})
	write("5", func(e map[string]json.RawMessage, _ *[]ViewEntry) { e["selection"] = raw("") })
	write("6", func(e map[string]json.RawMessage, vs *[]ViewEntry) {
		e["selection"] = raw("race")
		*vs = []ViewEntry{
			{Selection: "default", Present: true, Fingerprint: fingerprint("6")},
			{Selection: "race", Present: true, Fingerprint: fingerprint("7")},
		}
	})
	if got := Load(dir); len(got) != 1 || !got[0].Equal(good) {
		t.Fatalf("loaded %+v, want the one good record", got)
	}
}

// A record whose file name disagrees with the record inside is ignored
// — the store serves nothing it did not name — and an install temporary
// beside the records is never read and never swept: a concurrent
// installer's rename is about to claim it.
func TestMisnamedRecordsAndTemporariesAreNotRecords(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	rec := Record{Selection: "race", Symbol: "example.com/p.F", Views: views("race", "a"), Resolution: "resolved", Shape: "func", Package: "example.com/p", WitnessClass: "example"}
	if err := InstallAll(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	store, _ := StoreDir(dir)
	sameIdentity := rec.Clone()
	sameIdentity.Views = views("race", "e")
	if err := os.Rename(filepath.Join(store, mustName(t, rec)), filepath.Join(store, mustName(t, sameIdentity))); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); len(got) != 0 {
		t.Fatalf("a record under another fingerprint's name served: %+v", got)
	}
	// Live by identity and still collected: nothing serves it.
	if removed, kept, err := GC(t.Context(), dir, func(string, string) bool { return true }); err != nil || removed != 1 || kept != 0 {
		t.Fatalf("gc of the fingerprint-misnamed record = %d removed, %d kept, %v", removed, kept, err)
	}
	if err := InstallAll(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(store, mustName(t, rec)), filepath.Join(store, mustName(t, sameIdentity))); err != nil {
		t.Fatal(err)
	}
	other := Record{Selection: "plain", Symbol: "example.com/p.G", Views: views("plain", "a")}
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
	removed, kept, err := GC(t.Context(), dir, func(string, string) bool { return true })
	if err != nil || removed != 1 || kept != 0 {
		t.Fatalf("gc = %d removed, %d kept, %v; want the misnamed record alone removed", removed, kept, err)
	}
	if _, err := os.Stat(temp); err != nil {
		t.Fatalf("the temporary was swept: %v", err)
	}
}

func mustName(t *testing.T, rec Record) string {
	t.Helper()
	name, err := rec.FileName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}

// TestAdmitsAcceptsSourceResultTiersOnly pins the SourceResult tier admission
// (REQ-evidence-resolution-cache-format): a native SourceResult fingerprint
// is admitted, while any CodeResult/Measurement result kind, any missing
// source tier, or any observation, purity, dynamic-state, applicability,
// runtime, or machine tier is refused.
func TestAdmitsAcceptsSourceResultTiersOnly(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	digest := strings.Repeat("a", 32)
	source := fingerprint("a")
	if !Admits(source) {
		t.Fatalf("valid SourceResult fingerprint was refused: %+v", source)
	}
	for name, set := range map[string]func(*witnesscache.Fingerprint){
		"missing maximal closure":     func(f *witnesscache.Fingerprint) { f.MaximalClosure = "" },
		"missing compartment closure": func(f *witnesscache.Fingerprint) { f.TestVariantClosure = "" },
		"missing closure strategy":    func(f *witnesscache.Fingerprint) { f.ClosureStrategy = "" },
		"missing toolchain content":   func(f *witnesscache.Fingerprint) { f.ToolchainContent = "" },
		"missing admission policy":    func(f *witnesscache.Fingerprint) { f.AdmissionPolicy = "" },
		"missing toolchain guard":     func(f *witnesscache.Fingerprint) { f.Guards.Toolchain = "" },
		"missing build-config guard":  func(f *witnesscache.Fingerprint) { f.Guards.BuildConfig = "" },
		"code-result kind":            func(f *witnesscache.Fingerprint) { f.ResultKind = gofresh.CodeResult },
		"measurement kind":            func(f *witnesscache.Fingerprint) { f.ResultKind = gofresh.Measurement },
		"machine guard":               func(f *witnesscache.Fingerprint) { f.Guards.Machine = digest },
		"runtime-config guard":        func(f *witnesscache.Fingerprint) { f.Guards.RuntimeConfig = digest },
		"observation assertion":       func(f *witnesscache.Fingerprint) { f.ObservationAssertion = "pure" },
		"observation proof":           func(f *witnesscache.Fingerprint) { f.ObservationProof = gofresh.ObservationProof{Observable: true} },
		"purity assertion":            func(f *witnesscache.Fingerprint) { f.PurityAssertion = "pure" },
		"vouches":                     func(f *witnesscache.Fingerprint) { f.DynamicStateVouches = "x.Y" },
		"runtime inputs":              func(f *witnesscache.Fingerprint) { f.RuntimeInputs = digest },
		"runtime digest":              func(f *witnesscache.Fingerprint) { f.RuntimeDigest = digest },
		"single-subject discharges":   func(f *witnesscache.Fingerprint) { f.SingleSubjectDischarges = "x.Y" },
		"package-process discharges":  func(f *witnesscache.Fingerprint) { f.PackageProcessDischarges = "x.Y" },
		"dynamic-state strategy":      func(f *witnesscache.Fingerprint) { f.DynamicStateStrategy = gofresh.DynamicStateStrategy },
		"applicability extension": func(f *witnesscache.Fingerprint) {
			f.InertTestVariantApplicability = gofresh.InertTestVariantApplicability{
				Strategy:           gofresh.InertTestVariantExtension,
				TestVariantClosure: digest,
			}
		},
	} {
		one := source
		set(&one)
		if Admits(one) {
			t.Errorf("SourceResult admission accepted %s", name)
		}
		if err := InstallAll(t.TempDir(), []Record{{
			Selection:    "default",
			Symbol:       "example.com/p.F",
			Views:        []ViewEntry{{Selection: "default", Present: true, Fingerprint: one}},
			Resolution:   "resolved",
			Shape:        "shape",
			Package:      "example.com/p",
			WitnessClass: "example",
		}}); err == nil {
			t.Errorf("InstallAll accepted %s", name)
		}
	}
}

// TestAdmitsRecordPrecedenceAndSourceTierOracle checks AdmitsRecord and JSON
// round-trip admission against an independent precedence and tier reference
// oracle across arbitrary multi-view combinations.
func TestAdmitsRecordPrecedenceAndSourceTierOracle(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	rapid.Check(t, func(t *rapid.T) {
		selPool := []string{"", "default", "race", "integration", "submod"}
		resSel := rapid.SampledFrom(selPool).Draw(t, "resSel")
		pkg := rapid.SampledFrom([]string{"", "example.com/p", "example.com/other"}).Draw(t, "pkg")
		sym := rapid.SampledFrom([]string{"", "example.com/p.F", "example.com/p.", "example.com/other.G"}).Draw(t, "sym")
		resolution := rapid.SampledFrom([]string{"resolved", "generated_file", "not_found", ""}).Draw(t, "resolution")
		shape := rapid.SampledFrom([]string{"", "shape"}).Draw(t, "shape")
		wclass := rapid.SampledFrom([]string{"proof", "property", "example", "future", ""}).Draw(t, "wclass")
		nViews := rapid.IntRange(0, 4).Draw(t, "nViews")
		vs := make([]ViewEntry, nViews)
		for i := range nViews {
			vSel := rapid.SampledFrom(selPool).Draw(t, "vSel")
			present := rapid.Bool().Draw(t, "present")
			fpMode := rapid.IntRange(0, 3).Draw(t, "fpMode")
			var fp witnesscache.Fingerprint
			switch fpMode {
			case 0:
			case 1:
				fp = fingerprint("a")
			case 2:
				fp = fingerprint("b")
				fp.ResultKind = gofresh.CodeResult
			case 3:
				fp = fingerprint("c")
				fp.DynamicStateStrategy = gofresh.DynamicStateStrategy
			}
			vs[i] = ViewEntry{Selection: vSel, Present: present, Fingerprint: fp}
		}
		rec := Record{
			Selection:    resSel,
			Symbol:       sym,
			Views:        vs,
			Resolution:   resolution,
			Shape:        shape,
			Package:      pkg,
			WitnessClass: wclass,
		}

		oracle := func(r Record) bool {
			if r.Selection == "" || r.Symbol == "" || len(r.Views) == 0 {
				return false
			}
			if r.Resolution != "resolved" && r.Resolution != "generated_file" {
				return false
			}
			if r.Package == "" || !strings.HasPrefix(r.Symbol, r.Package+".") || len(r.Symbol) == len(r.Package)+1 {
				return false
			}
			if r.Resolution == "resolved" && r.Shape == "" {
				return false
			}
			if r.WitnessClass != "proof" && r.WitnessClass != "property" && r.WitnessClass != "example" {
				return false
			}
			seen := map[string]bool{}
			resIdx := -1
			for i, v := range r.Views {
				if v.Selection == "" || seen[v.Selection] {
					return false
				}
				seen[v.Selection] = true
				if v.Present {
					if v.Fingerprint != fingerprint("a") {
						return false
					}
				} else if v.Fingerprint != (witnesscache.Fingerprint{}) {
					return false
				}
				if v.Selection == r.Selection {
					resIdx = i
				}
			}
			if resIdx < 0 || !r.Views[resIdx].Present {
				return false
			}
			for i := 0; i < resIdx; i++ {
				if r.Views[i].Present {
					return false
				}
			}
			return true
		}

		want := oracle(rec)
		got := AdmitsRecord(rec)
		if got != want {
			t.Fatalf("AdmitsRecord(%+v) = %v, want %v", rec, got, want)
		}
		if want {
			name, err := rec.FileName()
			if err != nil {
				t.Fatalf("FileName(%+v): %v", rec, err)
			}
			raw, err := json.Marshal(entry{
				Version:      version,
				Selection:    rec.Selection,
				Symbol:       rec.Symbol,
				Views:        rec.Views,
				Resolution:   rec.Resolution,
				Shape:        rec.Shape,
				Package:      rec.Package,
				WitnessClass: rec.WitnessClass,
			})
			if err != nil {
				t.Fatalf("Marshal(%+v): %v", rec, err)
			}
			decoded, ok := decodeRecord(name, raw)
			if !ok || !decoded.Equal(rec) {
				t.Fatalf("decodeRecord round-trip = (%+v, %v), want (%+v, true)", decoded, ok, rec)
			}
		}
	})
}
