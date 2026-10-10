package witnesscache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/closure/testvariant"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/stipulator/stipulate"
)

func ledgerFingerprint(compartment string) Fingerprint {
	return Fingerprint{MaximalClosure: strings.Repeat("a", 32), TestVariantClosure: compartment,
		ClosureStrategy: gofresh.ClosureStrategy, DynamicStateStrategy: gofresh.DynamicStateStrategy,
		Guards:        guard.Guards{Toolchain: "go1.27.1", BuildConfig: strings.Repeat("b", 32)},
		RuntimeInputs: "eyJ2IjoyfQ", RuntimeDigest: "3a79bf37b571938d1f2907afb6a643f4", ResultKind: gofresh.CodeResult}
}

func simpleLedger(test string) *CompartmentLedger {
	return &CompartmentLedger{BindingStrategy: testvariant.BindingStrategy,
		Declarations: []CompartmentDeclaration{{File: "p_test.go", Package: "p", Kind: "func", Name: test, Hash: strings.Repeat("c", 32)}},
		FileHeaders:  []CompartmentFileHeader{{File: "p_test.go", Hash: strings.Repeat("d", 32), Bindings: &CompartmentFileBindings{Package: "p"}}}}
}

func TestLedgerBindingsRoundTripAndIndependentOwnership(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-ledger")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	bindings := func() *testvariant.TestVariantFileBindings {
		return &testvariant.TestVariantFileBindings{Package: "p", References: []string{"V", "testing"}, Imports: []testvariant.TestVariantImport{{Name: "testing", Path: "testing"}}}
	}
	original := gofresh.TestVariantLedger{BindingStrategy: testvariant.BindingStrategy,
		BaseFiles:    []gofresh.TestVariantFileHeader{{File: "p.go", Bindings: bindings()}},
		Declarations: []gofresh.TestVariantDeclaration{{File: "p_test.go", Kind: "func", Name: "TestA", Hash: strings.Repeat("a", 32), Package: "p", References: []string{"V"}}},
		FileHeaders:  []gofresh.TestVariantFileHeader{{File: "p_test.go", Hash: strings.Repeat("b", 32), Bindings: bindings()}, {File: "fixture", Hash: strings.Repeat("c", 32), Embedded: true}}}
	wire := LedgerFromGofresh(original)
	if back := wire.ToGofresh(); !reflect.DeepEqual(back, original) {
		t.Fatalf("conversion lost evidence: %#v", back)
	}
	rec := Record{Group: "group", Package: "example.com/p", Test: "TestA", Fingerprint: ledgerFingerprint(strings.Repeat("e", 32)), CompartmentLedger: wire, Outcomes: map[string]string{"example.com/p.TestA": "passed"}}
	if err := Install(t.Context(), dir, rec); err != nil {
		t.Fatal(err)
	}
	back := LoadLedger(dir, rec)
	if back == nil || !reflect.DeepEqual(back.ToGofresh(), original) {
		t.Fatalf("disk lost binding evidence: %#v", back)
	}
	// Both conversion directions own every nested mutable container.
	wire.BaseFiles[0].Bindings.References[0] = "changed"
	wire.BaseFiles[0].Bindings.Imports[0].Name = "changed"
	wire.FileHeaders[0].Bindings.References[0] = "changed"
	wire.FileHeaders[0].Bindings.Imports[0].Path = "changed"
	wire.Declarations[0].References[0] = "changed"
	if !reflect.DeepEqual(original, back.ToGofresh()) {
		t.Fatal("wire conversion aliased the source")
	}
	native := back.ToGofresh()
	native.BaseFiles[0].Bindings.References[0] = "native"
	native.BaseFiles[0].Bindings.Imports[0].Path = "native"
	native.FileHeaders[0].Bindings.References[0] = "native"
	native.FileHeaders[0].Bindings.Imports[0].Name = "native"
	native.Declarations[0].References[0] = "native"
	if !reflect.DeepEqual(original, back.ToGofresh()) {
		t.Fatal("native conversion aliased the wire")
	}
	// Nil and empty are distinct evidence in the base-file equality check.
	for _, empty := range []bool{false, true} {
		l := original.Clone()
		if empty {
			l.BaseFiles[0].Bindings.References = []string{}
			l.BaseFiles[0].Bindings.Imports = []testvariant.TestVariantImport{}
		} else {
			l.BaseFiles[0].Bindings.References = nil
			l.BaseFiles[0].Bindings.Imports = nil
		}
		data, err := json.Marshal(LedgerFromGofresh(l))
		if err != nil {
			t.Fatal(err)
		}
		var decoded CompartmentLedger
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(l, decoded.ToGofresh()) {
			t.Fatalf("nil/empty evidence moved: %s", data)
		}
	}
}

func TestLedgerCoordinatesSeparateCoreAndConfiguration(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-ledger", "REQ-evidence-store-gc")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	base := Record{Group: "group", Package: "example.com/p", Test: "TestA", Fingerprint: ledgerFingerprint(strings.Repeat("e", 32)), CompartmentLedger: simpleLedger("TestA"), Outcomes: map[string]string{"example.com/p.TestA": "passed"}}
	changes := []func(*Record){
		func(r *Record) {},
		func(r *Record) { r.Fingerprint.MaximalClosure = strings.Repeat("f", 32) },
		func(r *Record) { r.Fingerprint.Guards.BuildConfig = strings.Repeat("c", 32) },
		func(r *Record) { r.Fingerprint.Guards.Toolchain = "go1.27.2" },
		func(r *Record) { r.Fingerprint.ClosureStrategy = "other derivation" },
		func(r *Record) { r.Group = "other group" },
		func(r *Record) { r.Package = "example.com/q"; r.Outcomes = map[string]string{r.Key(): "passed"} },
		func(r *Record) {
			r.Fingerprint.InertTestVariantApplicability = gofresh.InertTestVariantApplicability{Strategy: gofresh.InertTestVariantExtension, TestVariantClosure: strings.Repeat("d", 32)}
		},
	}
	var records []Record
	for i, change := range changes {
		r := base
		change(&r)
		r.CompartmentLedger = simpleLedger("TestA")
		r.CompartmentLedger.BaseFiles = []CompartmentFileHeader{{File: "p.go", Bindings: &CompartmentFileBindings{Package: "p", References: []string{strings.Repeat("x", i+1)}}}}
		if err := Install(t.Context(), dir, r); err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	store, _ := StoreDir(dir)
	for _, r := range records {
		got := LoadLedger(dir, r)
		if !reflect.DeepEqual(got, r.CompartmentLedger) {
			t.Fatalf("coordinate aliased: %+v: %#v", coordinateOf(r), got)
		}
	}
	// Collection retains the exact coordinate named by a kept record,
	// including its effective rather than producing compartment.
	if _, _, err := GC(t.Context(), dir, func(pkg, test string) bool { return true }, nil); err != nil {
		t.Fatal(err)
	}
	kept := Load(t.Context(), dir)
	if len(kept) == 0 {
		t.Fatal("no retained records")
	}
	for _, r := range kept {
		if LoadLedger(dir, r) == nil {
			t.Fatalf("GC lost live ledger: %+v", coordinateOf(r))
		}
	}
	last := records[len(records)-1]
	if _, err := os.Stat(ledgerPath(store, coordinateOf(last).key())); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerRefusesAbsentUnknownAndPartialBindings(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-ledger", "REQ-evidence-witness-cache-format-version")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	r := Record{Group: "group", Package: "example.com/p", Test: "TestA", Fingerprint: ledgerFingerprint(strings.Repeat("e", 32)), CompartmentLedger: simpleLedger("TestA"), Outcomes: map[string]string{"example.com/p.TestA": "passed"}}
	if err := Install(t.Context(), dir, r); err != nil {
		t.Fatal(err)
	}
	store, _ := StoreDir(dir)
	path := ledgerPath(store, coordinateOf(r).key())
	for _, mutate := range []func(*ledgerEntry){
		func(e *ledgerEntry) { e.BindingStrategy = "" },
		func(e *ledgerEntry) { e.BindingStrategy = "unknown" },
		func(e *ledgerEntry) { e.FileHeaders[0].Bindings = nil },
		func(e *ledgerEntry) {
			e.FileHeaders[0].Embedded = true
			e.FileHeaders[0].Bindings.Package = ""
			e.Declarations[0].Package = ""
		},
		func(e *ledgerEntry) { e.BaseFiles = []CompartmentFileHeader{{File: "p.go"}} },
		func(e *ledgerEntry) { e.FileHeaders = nil },
		func(e *ledgerEntry) { e.FileHeaders = append(e.FileHeaders, e.FileHeaders[0]) },
		func(e *ledgerEntry) { e.Coordinate.BindingStrategy = "unknown" },
		func(e *ledgerEntry) { e.Version = 1 },
	} {
		e := ledgerEntry{Version: ledgerVersion, Coordinate: coordinateOf(r), CompartmentLedger: *simpleLedger("TestA")}
		mutate(&e)
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if got := LoadLedger(dir, r); got != nil {
			t.Fatalf("unproven ledger loaded: %s", data)
		}
	}
	for _, strategy := range []string{"", "unknown"} {
		missing := r
		missing.CompartmentLedger = LedgerFromGofresh(gofresh.TestVariantLedger{BindingStrategy: strategy})
		if missing.CompartmentLedger.BindingStrategy != strategy || missing.CompartmentLedger.ToGofresh().BindingStrategy != strategy {
			t.Fatal("conversion backfilled binding evidence")
		}
		if err := Install(t.Context(), dir, missing); err == nil {
			t.Fatal("writer installed unrecognized binding evidence")
		}
	}
	// A version-8 record is never reinterpreted as a supported recording.
	name := mustName(t, r)
	data, err := os.ReadFile(filepath.Join(store, name))
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"version": 9`, `"version": 8`, 1))
	if err := os.WriteFile(filepath.Join(store, name), data, 0600); err != nil {
		t.Fatal(err)
	}
	if got := Load(t.Context(), dir); len(got) != 0 {
		t.Fatal("old record backfilled")
	}
	// Load retains the reference even though the record refuses; explicit
	// GC removes it, while the young-file rule still protects new installs.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	Load(t.Context(), dir)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("refused record lost its sidecar", err)
	}
	if _, _, err := GC(t.Context(), dir, func(string, string) bool { return true }, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("old record's orphan survived GC: %v", err)
	}
}
