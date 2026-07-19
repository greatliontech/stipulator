package witnesscache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
		Package:     generated.ObservationProof.Subject.Package,
		Test:        generated.ObservationProof.Subject.Symbol,
		Fingerprint: FromGofresh(generated),
		Outcomes:    map[string]string{"example.com/cacheproof.TestObserved": "passed"},
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

	seedOne(Record{Package: rec.Package, Test: rec.Test, Outcomes: map[string]string{rec.Key(): "passed"}})
	requireAbsent("incomplete fingerprint")

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

	path = seedOne(rec)
	renamed := filepath.Join(filepath.Dir(path), identityDigest(rec.Package, rec.Test)+"-"+strings.Repeat("0", 16)+".json")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	requireAbsent("name disagreeing with content")
}

// TestStoreVariantsAndSiblings pins the per-record store's structure
// (REQ-evidence-witness-cache-format): a broken variant never discards a
// sibling record, one identity's distinct tree states coexist as
// variants, and the identity's variant set stays bounded with the oldest
// evicted first.
//
//gofresh:pure
func TestStoreVariantsAndSiblings(t *testing.T) {
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
		Package:     generated.ObservationProof.Subject.Package,
		Test:        generated.ObservationProof.Subject.Symbol,
		Fingerprint: FromGofresh(generated),
		Outcomes:    map[string]string{"example.com/cacheproof.TestObserved": "passed"},
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

	// A corrupt sibling file never discards the intact record.
	matches, err := filepath.Glob(filepath.Join(store, identityDigest(sibling.Package, sibling.Test)+"-*.json"))
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
		name := identityDigest(next.Package, next.Test) + "-" + fingerprintDigest(next.Fingerprint) + ".json"
		full := filepath.Join(store, name)
		stamp := time.Unix(int64(1_700_000_000+i*10), 0)
		if err := os.Chtimes(full, stamp, stamp); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		installed = append(installed, full)
	}
	matches, err = filepath.Glob(filepath.Join(store, identityDigest(rec.Package, rec.Test)+"-*.json"))
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
	want := generatedObservationFingerprint(t)
	want.Guards = guard.Guards{Toolchain: "toolchain", BuildConfig: "build"}
	want.PurityAssertion = "source directive"
	want.RuntimeInputs = "manifest"
	want.RuntimeDigest = "digest"
	if got := FromGofresh(want).ToGofresh(); !reflect.DeepEqual(got, want) {
		t.Fatalf("fingerprint round trip = %+v, want %+v", got, want)
	}
}
