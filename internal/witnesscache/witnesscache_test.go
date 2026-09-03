package witnesscache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/stipulator/stipulate"
)

func generatedObservationFingerprint(t *testing.T) gofresh.Fingerprint {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":        "module example.com/cacheproof\n\ngo 1.26\n",
		"data.txt":      "observed input",
		"proof_test.go": "package cacheproof\n\nimport (\"os\"; \"testing\")\n\nfunc TestObserved(*testing.T) { _, _ = os.ReadFile(\"data.txt\") }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	subject := gofresh.Subject{Package: "example.com/cacheproof", Symbol: "TestObserved"}
	// The fixture module must resolve regardless of the invoking process's
	// workspace: under a witness run the ambient environment pins GOWORK to
	// the repository workspace, which cannot provide the fixture package.
	env := []string{"GOWORK=off"}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOWORK=") {
			env = append(env, entry)
		}
	}
	engine, err := gofresh.New(gofresh.WithDir(dir), gofresh.WithEnv(env...))
	if err != nil {
		t.Fatal(err)
	}
	view, err := engine.NewView(context.Background(), []gofresh.Subject{subject}, dir)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := view.CaptureObserved(context.Background(), subject)
	if err != nil {
		t.Fatal(err)
	}
	if !fingerprint.ObservationProof.Observable {
		t.Fatalf("generated proof is not positive: %+v", fingerprint.ObservationProof)
	}
	return fingerprint
}

// TestLoadUnreadableIsEmpty pins the unreadable-record leg of
// REQ-evidence-witness-freshness and the per-record refusal of
// REQ-evidence-witness-cache-format: a corrupt, version-mismatched,
// misnamed, or structurally invalid variant file is that record alone
// absent, so its test runs — a broken record costs work, never
// correctness.
//
//gofresh:pure
func TestLoadUnreadableIsEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("measured heavy under the fast tier (in-process)")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-evidence-witness-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if got := Load(dir); got != nil {
		t.Fatalf("absent store loaded %d records", len(got))
	}

	// seedOne resets the store to exactly one installed record and
	// returns its variant file path for tampering.
	seedOne := func(r Record) string {
		t.Helper()
		if err := os.RemoveAll(store); err != nil {
			t.Fatal(err)
		}
		if err := Install(dir, r); err != nil {
			t.Fatal(err)
		}
		matches, err := filepath.Glob(filepath.Join(store, "*.json"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("store files = %v (%v), want exactly one", matches, err)
		}
		return matches[0]
	}
	tamper := func(path, old, new string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		replaced := strings.Replace(string(data), old, new, 1)
		if replaced == string(data) {
			t.Fatalf("tamper target %q not found", old)
		}
		if err := os.WriteFile(path, []byte(replaced), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	requireAbsent := func(what string) {
		t.Helper()
		if got := Load(dir); got != nil {
			t.Fatalf("%s loaded %d records", what, len(got))
		}
	}

	generated := generatedObservationFingerprint(t)
	generated.RuntimeInputs = "eyJ2IjoxfQ"
	generated.RuntimeDigest = "3a79bf37b571938d1f2907afb6a643f4"
	rec := Record{
		Group:       "6772702d64696765",
		Package:     generated.ObservationProof.Subject.Package,
		Test:        generated.ObservationProof.Subject.Symbol,
		Fingerprint: FromGofresh(generated),
		CompartmentLedger: &CompartmentLedger{
			Declarations: []CompartmentDeclaration{{File: "observed_test.go", Kind: "func", Name: "TestObserved", Hash: "00112233445566778899aabbccddeeff"}},
			FileHeaders:  []CompartmentFileHeader{{File: "observed_test.go", Hash: "ffeeddccbbaa99887766554433221100"}},
		},
		Outcomes: map[string]string{"example.com/cacheproof.TestObserved": "passed"},
	}
	path := seedOne(rec)
	got := Load(dir)
	if len(got) != 1 || got[0].Key() != rec.Key() {
		t.Fatalf("round trip lost the record: %+v", got)
	}

	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	requireAbsent("corrupt file")

	path = seedOne(rec)
	tamper(path, fmt.Sprintf(`"version": %d`, version), fmt.Sprintf(`"version": %d`, version+1))
	requireAbsent("version-mismatched file")

	seedOne(Record{Group: rec.Group, Package: rec.Package, Test: rec.Test, Outcomes: map[string]string{rec.Key(): "passed"}})
	requireAbsent("incomplete fingerprint")

	groupless := rec
	groupless.Group = ""
	seedOne(groupless)
	requireAbsent("record without a producing group")

	broken := rec
	broken.Fingerprint.ResultKind = 0
	seedOne(broken)
	requireAbsent("missing result kind")

	broken = rec
	broken.Fingerprint.RuntimeInputs = "not-base64"
	seedOne(broken)
	requireAbsent("malformed runtime manifest")

	broken = rec
	broken.Outcomes = nil
	seedOne(broken)
	requireAbsent("outcomeless record")

	broken = rec
	broken.Outcomes = map[string]string{rec.Key(): "passed", "p.Other": "passed"}
	seedOne(broken)
	requireAbsent("foreign outcome")

	path = seedOne(rec)
	tamper(path, `"resultKind": 1`, `"machine": "", "resultKind": 1`)
	requireAbsent("explicit measurement field")

	otherProof := *rec.Fingerprint.ObservationProof
	otherProof.Symbol = "Other"
	broken = rec
	broken.Fingerprint.ObservationProof = &otherProof
	seedOne(broken)
	requireAbsent("proof for another subject")

	badEvidence := *rec.Fingerprint.ObservationProof
	badEvidence.Evidence = "proof"
	broken = rec
	broken.Fingerprint.ObservationProof = &badEvidence
	seedOne(broken)
	requireAbsent("malformed proof evidence")

	posWithReason := *rec.Fingerprint.ObservationProof
	posWithReason.Reason = "blocked"
	broken = rec
	broken.Fingerprint.ObservationProof = &posWithReason
	seedOne(broken)
	requireAbsent("positive proof with a reason")

	negNoReason := *rec.Fingerprint.ObservationProof
	negNoReason.Observable = false
	negNoReason.Reason = ""
	broken = rec
	broken.Fingerprint.ObservationProof = &negNoReason
	seedOne(broken)
	requireAbsent("negative proof without a reason")

	withoutObservation := rec
	withoutObservation.Fingerprint.ObservationAssertion = ""
	withoutObservation.Fingerprint.ObservationProof = nil
	path = seedOne(withoutObservation)
	tamper(path, `"runtimeInputs":`, `"observationAssertion": null, "runtimeInputs":`)
	requireAbsent("null observation assertion")

	path = seedOne(rec)
	tamper(path, `"observable": true,`, `"observable": true, "reason": null,`)
	requireAbsent("null positive-proof reason")

	path = seedOne(rec)
	tamper(path, `"observable": true,`, `"observable": true, "reason": "",`)
	requireAbsent("positive proof with explicit empty reason")

	negative := rec
	negativeProof := *rec.Fingerprint.ObservationProof
	negativeProof.Observable = false
	negativeProof.Reason = "blocked"
	negative.Fingerprint.ObservationProof = &negativeProof
	path = seedOne(negative)
	tamper(path, `"observable": false,`, `"observable": null, "observable": false,`)
	requireAbsent("proof with duplicate observable")

	path = seedOne(negative)
	tamper(path, `"observable": false,`, ``)
	requireAbsent("proof without observable")

	path = seedOne(negative)
	tamper(path, `"observable": false`, `"observable": null`)
	requireAbsent("proof with null observable")

	pure := rec
	pure.Fingerprint.PurityAssertion = "source directive"
	path = seedOne(pure)
	tamper(path, `"purityAssertion": "source directive"`, `"purityAssertion": null`)
	requireAbsent("null purity")

	path = seedOne(rec)
	tamper(path, `"outcomes":`, `"registrations": null, "outcomes":`)
	requireAbsent("null registrations")

	// The ledger is the carve-out's, held in the ledger store: a record
	// without one loads and serves on plain validity.
	broken = rec
	broken.CompartmentLedger = nil
	seedOne(broken)
	if got := Load(dir); len(got) != 1 {
		t.Fatalf("ledgerless record loaded %d records, want 1", len(got))
	}
	// A prior version's record carried the ledger inline; the field is
	// unknown now, so it fails closed.
	path = seedOne(rec)
	tamper(path, `"outcomes":`, `"compartmentLedger": {}, "outcomes":`)
	requireAbsent("record carrying an inline ledger")

	withVariantPin := rec
	withVariantPin.Fingerprint.TestVariantClosure = "zz112233445566778899aabbccddeeff"
	seedOne(withVariantPin)
	requireAbsent("malformed test-variant closure digest")

	path = seedOne(rec)
	renamed := filepath.Join(filepath.Dir(path), identityDigest(rec.Group, rec.Package, rec.Test)+"-"+strings.Repeat("0", 16)+".json")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	requireAbsent("name disagreeing with content")
}

// TestLedgerStoreRefusesPerFile pins the ledger store
// (REQ-evidence-witness-cache-format, REQ-evidence-witness-freshness's
// carve-out base): one file per compartment digest, written once, read
// back for the record's own test only — a malformed file, another
// version, a name-content disagreement, a ledger omitting the record's
// declaration, or an absent file is no ledger, which costs the carve-out
// alone.
//
//gofresh:pure
func TestLedgerStoreRefusesPerFile(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format", "REQ-evidence-witness-freshness")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	digest := "0123456789abcdef0123456789abcdef"
	ledger := &CompartmentLedger{
		Declarations: []CompartmentDeclaration{
			{File: "p_test.go", Kind: "func", Name: "TestA", Hash: "00112233445566778899aabbccddeeff", Package: "p", References: []string{"testing"}},
			{File: "p_test.go", Kind: "method", Name: "M", Receiver: "T", Hash: "00112233445566778899aabbccddeeff"},
		},
		FileHeaders: []CompartmentFileHeader{{File: "p_test.go", Hash: "ffeeddccbbaa99887766554433221100"}, {File: "fixture.txt", Hash: "ffeeddccbbaa99887766554433221100", Embedded: true}},
	}
	rec := Record{Group: "6772702d64696765", Package: "example.com/p", Test: "TestA", Fingerprint: Fingerprint{MaximalClosure: "aa", TestVariantClosure: digest}, CompartmentLedger: ledger, Outcomes: map[string]string{"example.com/p.TestA": "passed"}}
	if got := LoadLedger(dir, digest, "TestA"); got != nil {
		t.Fatalf("absent ledger loaded %+v", got)
	}
	if err := Install(dir, rec); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store, "ledgers", digest+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("install left no ledger file: %v", err)
	}
	if got := LoadLedger(dir, digest, "TestA"); !reflect.DeepEqual(got, ledger) {
		t.Fatalf("ledger round trip = %+v, want %+v", got, ledger)
	}
	if got := LoadLedger(dir, digest, "TestOther"); got != nil {
		t.Fatalf("a ledger not declaring the record's test loaded for it: %+v", got)
	}
	if got := LoadLedger(dir, "M", "M"); got != nil {
		t.Fatalf("a method entry counted as the test's own declaration: %+v", got)
	}
	// Write-once: the digest addresses the content, so a later install
	// under the same digest never rewrites the file.
	other := rec
	other.Test = "TestB"
	other.Outcomes = map[string]string{"example.com/p.TestB": "passed"}
	other.CompartmentLedger = &CompartmentLedger{Declarations: []CompartmentDeclaration{{File: "p_test.go", Kind: "func", Name: "TestB", Hash: "00112233445566778899aabbccddeeff"}}}
	if err := Install(dir, other); err != nil {
		t.Fatal(err)
	}
	if got := LoadLedger(dir, digest, "TestA"); !reflect.DeepEqual(got, ledger) {
		t.Fatalf("a second install rewrote the ledger: %+v", got)
	}
	if residue, _ := filepath.Glob(filepath.Join(store, "ledgers", ".ledger-*")); len(residue) != 0 {
		t.Fatalf("ledger install temporaries persist: %v", residue)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tamper := func(what, old, new string) {
		t.Helper()
		replaced := strings.Replace(string(original), old, new, 1)
		if replaced == string(original) {
			t.Fatalf("%s: tamper target %q not found", what, old)
		}
		if err := os.WriteFile(path, []byte(replaced), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := LoadLedger(dir, digest, "TestA"); got != nil {
			t.Fatalf("%s loaded %+v", what, got)
		}
	}
	tamper("another version", `"version": 1`, `"version": 2`)
	tamper("name disagreeing with content", digest, "ffffffffffffffffffffffffffffffff")
	tamper("malformed declaration digest", `"hash": "00112233445566778899aabbccddeeff",
      "package"`, `"hash": "not-a-digest",
      "package"`)
	tamper("header without a file", `"file": "fixture.txt"`, `"file": ""`)
	tamper("unknown field", `"version": 1`, `"version": 1, "extra": true`)
	if err := os.WriteFile(path, []byte("{ torn"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadLedger(dir, digest, "TestA"); got != nil {
		t.Fatalf("torn ledger loaded %+v", got)
	}
	if got := LoadLedger(dir, "not-a-digest", "TestA"); got != nil {
		t.Fatalf("malformed digest loaded %+v", got)
	}
	// A present file that does not read back as a ledger — a prior
	// version's, a torn one — is rewritten by the next install of its
	// compartment; a refused file never outlives that install.
	if err := os.WriteFile(path, []byte(strings.Replace(string(original), `"version": 1`, `"version": 0`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir, rec); err != nil {
		t.Fatal(err)
	}
	if got := LoadLedger(dir, digest, "TestA"); !reflect.DeepEqual(got, ledger) {
		t.Fatalf("a refused ledger file survived its compartment's next install: %+v", got)
	}
}

// TestLoadReclaimsUnreferencedLedgers pins the ledger store's bound
// (REQ-evidence-witness-cache-format): a ledger no record file names
// — its records evicted past the variant bound — is reclaimed as the
// store loads, while a ledger a refused record still names stays, so
// the ledger store never outgrows the record store.
//
//gofresh:pure
func TestLoadReclaimsUnreferencedLedgers(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := Record{Group: "6772702d64696765", Package: "example.com/p", Test: "TestA", Fingerprint: Fingerprint{MaximalClosure: "0123456789abcdef0123456789abcdef", Toolchain: "go1.26", BuildConfig: "00112233445566778899aabbccddeeff", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "3a79bf37b571938d1f2907afb6a643f4", ResultKind: gofresh.CodeResult}, Outcomes: map[string]string{"example.com/p.TestA": "passed"}}
	ledgerOf := func(digest string) *CompartmentLedger {
		return &CompartmentLedger{Declarations: []CompartmentDeclaration{{File: "p_test.go", Kind: "func", Name: "TestA", Hash: digest}}}
	}
	// One identity under more compartments than the variant bound holds:
	// the oldest records evict, their ledgers become unreferenced.
	var digests []string
	for i := 0; i < variantBound+2; i++ {
		digest := fmt.Sprintf("%032x", i+1)
		digests = append(digests, digest)
		rec := base
		rec.Fingerprint.TestVariantClosure = digest
		rec.CompartmentLedger = ledgerOf(digest)
		if err := Install(dir, rec); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(int64(1_700_000_000+i*10), 0)
		if err := os.Chtimes(filepath.Join(store, fileName(rec)), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	// Eviction runs on install; the ledgers stay until a load.
	if ledgers, _ := filepath.Glob(filepath.Join(store, "ledgers", "*.json")); len(ledgers) != variantBound+2 {
		t.Fatalf("ledgers before load = %d, want %d", len(ledgers), variantBound+2)
	}
	// A refused record — a prior version — still keeps its ledger
	// referenced: the refusal is this file's, not its compartment's.
	refused := base
	refused.Fingerprint.TestVariantClosure = strings.Repeat("f", 32)
	refused.CompartmentLedger = ledgerOf(refused.Fingerprint.TestVariantClosure)
	if err := Install(dir, refused); err != nil {
		t.Fatal(err)
	}
	refusedPath := filepath.Join(store, fileName(refused))
	data, err := os.ReadFile(refusedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(refusedPath, []byte(strings.Replace(string(data), fmt.Sprintf(`"version": %d`, version), `"version": 1`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	// The refused install took the newest slot: the bound keeps it and
	// the three newest stamped variants, and evicts the three oldest.
	got := Load(dir)
	if len(got) != variantBound-1 {
		t.Fatalf("loaded %d records, want the %d valid ones the bound keeps", len(got), variantBound-1)
	}
	for _, digest := range digests[:3] {
		if _, err := os.Stat(filepath.Join(store, "ledgers", digest+".json")); !os.IsNotExist(err) {
			t.Fatalf("evicted variant's ledger %s survived the load: %v", digest, err)
		}
	}
	for _, digest := range append(digests[3:], refused.Fingerprint.TestVariantClosure) {
		if _, err := os.Stat(filepath.Join(store, "ledgers", digest+".json")); err != nil {
			t.Fatalf("referenced ledger %s reclaimed: %v", digest, err)
		}
	}
	// A ledger younger than the load is a concurrent install's, its
	// record about to land: spared now, reclaimed once it has aged
	// unreferenced. The load's start is taken an hour back so the file
	// written here stands in for one written during the load.
	young := filepath.Join(store, "ledgers", strings.Repeat("e", 32)+".json")
	if err := os.WriteFile(young, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	loadSince(dir, time.Now().Add(-time.Hour))
	if _, err := os.Stat(young); err != nil {
		t.Fatalf("a ledger younger than the load was reclaimed: %v", err)
	}
	old := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(young, old, old); err != nil {
		t.Fatal(err)
	}
	Load(dir)
	if _, err := os.Stat(young); !os.IsNotExist(err) {
		t.Fatalf("an aged unreferenced ledger survived the load: %v", err)
	}
}

// TestLoadOrdersVariantsNewestFirst pins serving's first try
// (REQ-evidence-witness-cache-format): one identity's variants load most
// recently installed first, by install time rather than name, so the
// variant the last state change produced is the first checked.
//
//gofresh:pure
func TestLoadOrdersVariantsNewestFirst(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := Record{Group: "6772702d64696765", Package: "example.com/p", Test: "TestA", Fingerprint: Fingerprint{TestVariantClosure: "0123456789abcdef0123456789abcdef", Toolchain: "go1.26", BuildConfig: "00112233445566778899aabbccddeeff", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "3a79bf37b571938d1f2907afb6a643f4", ResultKind: gofresh.CodeResult}, Outcomes: map[string]string{"example.com/p.TestA": "passed"}}
	// Install order and name order both disagree with the stamps: the
	// stamps alone decide.
	closures := []string{"cccccccccccccccccccccccccccccccc", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	stamps := []int64{1_700_000_020, 1_700_000_000, 1_700_000_010}
	for i, closure := range closures {
		rec := base
		rec.Fingerprint.MaximalClosure = closure
		if err := Install(dir, rec); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(stamps[i], 0)
		if err := os.Chtimes(filepath.Join(store, fileName(rec)), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, rec := range Load(dir) {
		got = append(got, rec.Fingerprint.MaximalClosure)
	}
	want := []string{closures[0], closures[2], closures[1]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("load order = %v, want newest first %v", got, want)
	}
}

// TestStoreVariantsAndSiblings pins the per-record store's structure
// (REQ-evidence-witness-cache-format): a broken variant never discards a
// sibling record, one identity's distinct tree states coexist as
// variants, and the identity's variant set stays bounded with the oldest
// evicted first.
//
//gofresh:pure
func TestStoreVariantsAndSiblings(t *testing.T) {
	if testing.Short() {
		t.Skip("measured heavy under the fast tier (in-process)")
	}
	stipulate.Covers(t, "REQ-evidence-witness-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	generated := generatedObservationFingerprint(t)
	generated.RuntimeInputs = "eyJ2IjoxfQ"
	generated.RuntimeDigest = "3a79bf37b571938d1f2907afb6a643f4"
	rec := Record{
		Group:       "6772702d64696765",
		Package:     generated.ObservationProof.Subject.Package,
		Test:        generated.ObservationProof.Subject.Symbol,
		Fingerprint: FromGofresh(generated),
		// One compartment, shared by both tests of the package.
		CompartmentLedger: &CompartmentLedger{
			Declarations: []CompartmentDeclaration{
				{File: "observed_test.go", Kind: "func", Name: "TestObserved", Hash: "00112233445566778899aabbccddeeff"},
				{File: "observed_test.go", Kind: "func", Name: "TestSibling", Hash: "00112233445566778899aabbccddeeff"},
			},
			FileHeaders: []CompartmentFileHeader{{File: "observed_test.go", Hash: "ffeeddccbbaa99887766554433221100"}},
		},
		Outcomes: map[string]string{"example.com/cacheproof.TestObserved": "passed"},
	}
	sibling := rec
	sibling.Test = "TestSibling"
	siblingProof := *rec.Fingerprint.ObservationProof
	siblingProof.Symbol = "TestSibling"
	sibling.Fingerprint.ObservationProof = &siblingProof
	sibling.Outcomes = map[string]string{sibling.Key(): "passed"}
	if err := Install(dir, rec); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir, sibling); err != nil {
		t.Fatal(err)
	}
	// The shared compartment's ledger is stored once, readable for each
	// of its tests.
	if ledgers, _ := filepath.Glob(filepath.Join(store, "ledgers", "*.json")); len(ledgers) != 1 {
		t.Fatalf("ledger files = %v, want one per compartment", ledgers)
	}
	for _, test := range []string{rec.Test, sibling.Test} {
		if LoadLedger(dir, rec.Fingerprint.TestVariantClosure, test) == nil {
			t.Fatalf("the shared ledger does not load for %s", test)
		}
	}

	// A corrupt sibling file never discards the intact record.
	matches, err := filepath.Glob(filepath.Join(store, identityDigest(sibling.Group, sibling.Package, sibling.Test)+"-*.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("sibling variants = %v (%v), want one", matches, err)
	}
	if err := os.WriteFile(matches[0], []byte("{ torn"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Load(dir)
	if len(got) != 1 || got[0].Key() != rec.Key() {
		t.Fatalf("sibling corruption discarded the intact record: %+v", got)
	}

	// Distinct tree states of one identity coexist as variants.
	variant := rec
	variant.Fingerprint.MaximalClosure = "ffeeddccbbaa99887766554433221100"
	if err := Install(dir, variant); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, r := range Load(dir) {
		if r.Key() == rec.Key() {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("identity variants loaded = %d, want 2", count)
	}

	// The identity's variant set stays bounded, and eviction is by
	// recency: the oldest installs go first, the newest survive. Mtimes
	// are pinned explicitly so filesystem granularity cannot blur order.
	var installed []string
	for i := 0; i < variantBound+2; i++ {
		next := rec
		next.Fingerprint.MaximalClosure = fmt.Sprintf("%032x", i+1)
		if err := Install(dir, next); err != nil {
			t.Fatal(err)
		}
		name := identityDigest(next.Group, next.Package, next.Test) + "-" + fingerprintDigest(next.Fingerprint) + ".json"
		full := filepath.Join(store, name)
		stamp := time.Unix(int64(1_700_000_000+i*10), 0)
		if err := os.Chtimes(full, stamp, stamp); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		installed = append(installed, full)
	}
	matches, err = filepath.Glob(filepath.Join(store, identityDigest(rec.Group, rec.Package, rec.Test)+"-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) > variantBound {
		t.Fatalf("identity holds %d variants, want at most %d", len(matches), variantBound)
	}
	if _, err := os.Stat(installed[len(installed)-1]); err != nil {
		t.Fatalf("newest install evicted: %v", err)
	}
	if _, err := os.Stat(installed[0]); !os.IsNotExist(err) {
		t.Fatalf("oldest install survived recency eviction: %v", err)
	}
	// Atomic installs leave no temporaries behind.
	if residue, _ := filepath.Glob(filepath.Join(store, ".variant-*")); len(residue) != 0 {
		t.Fatalf("install temporaries persist: %v", residue)
	}
}

//gofresh:pure
func TestFingerprintRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("measured heavy under the fast tier (in-process)")
	}
	stipulate.Covers(t, "REQ-evidence-witness-cache-format")
	positive := generatedObservationFingerprint(t)
	if positive.DynamicStateStrategy == "" {
		// The engine's contract: every capture records the strategy its
		// evidence was computed under. An empty value here would retire
		// this test's coverage of the validity field silently.
		t.Fatal("engine capture carries no dynamic-state strategy")
	}
	seed := func(fp gofresh.Fingerprint) gofresh.Fingerprint {
		fp.Guards = guard.Guards{Toolchain: "toolchain", BuildConfig: "build", Machine: "machine", RuntimeConfig: "runtime"}
		fp.PurityAssertion = "source directive"
		fp.DynamicStateVouches = "a.example/dep.Var,b.example/dep.W"
		// Attestation-borne discharges: not armed by any engine this repo
		// constructs (the field walk below fails when one arrives
		// unseeded), but the round trip must stay faithful so an
		// adoption cannot silently drop them.
		fp.SingleSubjectDischarges = "s.example/dep.One"
		fp.PackageProcessDischarges = "p.example/dep.Two"
		fp.RuntimeInputs = "manifest"
		fp.RuntimeDigest = "digest"
		return fp
	}
	// Two seeds because a legal record cannot populate every proof field
	// at once (a positive proof carries no reason): the engine's positive
	// observation proof, and a negative proof whose Reason is set.
	negative := positive
	negative.ObservationProof = gofresh.ObservationProof{
		Strategy:   positive.ObservationProof.Strategy,
		Subject:    positive.ObservationProof.Subject,
		Observable: false,
		Reason:     "operation outside the observed set",
	}
	cases := []gofresh.Fingerprint{seed(positive), seed(negative)}
	for i, want := range cases {
		if got := FromGofresh(want).ToGofresh(); !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d: fingerprint round trip = %+v, want %+v", i, got, want)
		}
	}

	// The wire form is a hand-maintained mirror of gofresh.Fingerprint:
	// walk its fields recursively (nested STRUCT VALUES included —
	// pointer/slice/interface fields would read as leaves, where a
	// nil/empty value still fires) and require every leaf non-zero in
	// at least one seed, so a field gofresh grows arrives here as a
	// failure forcing the record-or-exempt decision instead of riding
	// unrecorded through the next bump.
	seeded := map[string]bool{}
	for _, c := range cases {
		collectSeededLeaves("", reflect.ValueOf(c), seeded)
	}
	for path, nonZero := range seeded {
		if !nonZero {
			t.Errorf("fingerprint field %s is zero in every round-trip seed: wire it (Fingerprint struct + both converters) and seed it, or exempt it here with the reason", path)
		}
	}
}

// collectSeededLeaves ORs each struct leaf's non-zeroness into acc:
// order-independent across seeds, and a leaf never visited reads false
// and errors (fail-closed).
func collectSeededLeaves(prefix string, v reflect.Value, acc map[string]bool) {
	for i := range v.NumField() {
		f := v.Field(i)
		name := prefix + v.Type().Field(i).Name
		if f.Kind() == reflect.Struct {
			collectSeededLeaves(name+".", f, acc)
			continue
		}
		acc[name] = acc[name] || !f.IsZero()
	}
}

// TestFingerprintWireKeySet binds REQ-evidence-witness-cache-format's
// fingerprint key enumeration to the marshalled wire: the persisted key
// set of a fingerprint populated to that enumeration is exactly the
// spec's list, so a
// new field or an accidental key rename (which would silently orphan
// every stored record) fails here instead of drifting past review.
func TestFingerprintWireKeySet(t *testing.T) {
	if testing.Short() {
		t.Skip("measured heavy under the fast tier (in-process)")
	}
	stipulate.Covers(t, "REQ-evidence-witness-cache-format")
	want := generatedObservationFingerprint(t)
	// Populated to the spec's persisted key set, NOT every struct field:
	// machine and runtimeConfig are measurement guards the code-record
	// format forbids (UnmarshalJSON refuses them), deliberately unseeded
	// here so they never marshal — the converters themselves stay
	// format-agnostic (the round trip seeds them), and the record
	// boundary is where the format's exclusion is enforced. The expected
	// list is the enumeration in docs/specs/evidence.md's
	// REQ-evidence-witness-cache-format, byte-for-byte.
	want.Guards = guard.Guards{Toolchain: "toolchain", BuildConfig: "build"}
	want.PurityAssertion = "source directive"
	want.DynamicStateVouches = "a.example/dep.Var"
	want.SingleSubjectDischarges = "s.example/dep.One"
	want.PackageProcessDischarges = "p.example/dep.Two"
	want.RuntimeInputs = "manifest"
	want.RuntimeDigest = "digest"
	data, err := json.Marshal(FromGofresh(want))
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	spec := []string{
		"maximalClosure", "testVariantClosure", "toolchain", "buildConfig",
		"observationAssertion", "observationProof", "purityAssertion",
		"dynamicStateVouches", "singleSubjectDischarges",
		"packageProcessDischarges", "dynamicStateStrategy",
		"runtimeInputs", "runtimeDigest", "resultKind",
	}
	specSet := map[string]bool{}
	for _, k := range spec {
		specSet[k] = true
	}
	for k := range keys {
		if !specSet[k] {
			t.Errorf("marshalled key %q is not in REQ-evidence-witness-cache-format's enumeration", k)
		}
	}
	for _, k := range spec {
		if _, ok := keys[k]; !ok {
			t.Errorf("spec-enumerated key %q absent from the populated wire", k)
		}
	}
}

// The store GC drops departed identities and unreadable entries, keeps
// live ones, and is the only cross-identity eviction
// (REQ-evidence-store-gc).
func TestWitnessStoreGCDropsDepartedIdentities(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	ledgerOf := func(test string) *CompartmentLedger {
		return &CompartmentLedger{Declarations: []CompartmentDeclaration{{File: "p_test.go", Kind: "func", Name: test, Hash: "00112233445566778899aabbccddeeff"}}}
	}
	digests := map[string]string{"TestLive": strings.Repeat("a", 32), "TestDeparted": strings.Repeat("b", 32)}
	install := func(pkg, test string) {
		t.Helper()
		if err := Install(dir, Record{Group: "6772702d64696765", Package: pkg, Test: test, Outcomes: map[string]string{pkg + "." + test: "passed"}, Fingerprint: Fingerprint{MaximalClosure: "aa", TestVariantClosure: digests[test]}, CompartmentLedger: ledgerOf(test)}); err != nil {
			t.Fatal(err)
		}
	}
	install("example.com/p", "TestLive")
	install("example.com/p", "TestDeparted")
	// A live test under a retired coordinate: liveness alone keeps it,
	// coordinate retirement removes it — its compartment's ledger, which
	// no kept record names, with it.
	retired := strings.Repeat("c", 32)
	if err := Install(dir, Record{Group: "feedfeedfeedfeed", Package: "example.com/p", Test: "TestLive", Outcomes: map[string]string{"example.com/p.TestLive": "passed"}, Fingerprint: Fingerprint{MaximalClosure: "aa", TestVariantClosure: retired}, CompartmentLedger: ledgerOf("TestLive")}); err != nil {
		t.Fatal(err)
	}
	store, err := StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "garbage.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "ledgers", "garbage.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, kept, err := GC(dir, func(pkg, test string) bool {
		return pkg == "example.com/p" && test == "TestLive"
	}, func(group string) bool { return group == "6772702d64696765" })
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 || kept != 1 {
		t.Fatalf("gc = %d removed, %d kept; want 3 (departed identity, garbage, retired coordinate), 1", removed, kept)
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name()+"/")
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) != 2 || names[0] != identityDigest("6772702d64696765", "example.com/p", "TestLive")+"-"+fingerprintDigest(Fingerprint{MaximalClosure: "aa", TestVariantClosure: digests["TestLive"]})+".json" || names[1] != "ledgers/" {
		t.Fatalf("post-gc store entries = %v, want only the live identity's variant beside the ledger store", names)
	}
	ledgers, err := os.ReadDir(filepath.Join(store, "ledgers"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ledgers) != 1 || ledgers[0].Name() != digests["TestLive"]+".json" {
		t.Fatalf("post-gc ledgers = %v, want only the kept record's compartment", ledgers)
	}
	// A ledger younger than the collection is a concurrent install's,
	// its record about to land: spared, as under a load.
	young := filepath.Join(store, "ledgers", strings.Repeat("e", 32)+".json")
	if err := os.WriteFile(young, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := gcSince(dir, func(pkg, test string) bool { return test == "TestLive" }, nil, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(young); err != nil {
		t.Fatalf("a ledger younger than the collection was reclaimed: %v", err)
	}
	if _, _, err := GC(dir, func(pkg, test string) bool { return test == "TestLive" }, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(young); !os.IsNotExist(err) {
		t.Fatalf("an aged unreferenced ledger survived the collection: %v", err)
	}

	// Removal failures surface beside the partial counts - a clean pass
	// must never be reported over an undeletable entry.
	if runtime.GOOS != "windows" {
		install("example.com/p", "TestStuck")
		if err := os.Chmod(store, 0o555); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(store, 0o755)
		removed, kept, err := GC(dir, func(string, string) bool { return false }, nil)
		if err == nil {
			t.Fatalf("undeletable entries reported clean: %d removed, %d kept", removed, kept)
		}
	}
}
