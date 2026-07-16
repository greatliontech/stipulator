package witnesscache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
	engine, err := gofresh.New(gofresh.WithDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	view, err := engine.NewView([]gofresh.Subject{subject}, dir)
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
// REQ-evidence-witness-freshness: a corrupt or version-mismatched cache is
// an empty cache, so every test runs — a broken cache costs work, never
// correctness.
//
//gofresh:pure
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

	generated := generatedObservationFingerprint(t)
	generated.RuntimeInputs = "eyJ2IjoxfQ"
	generated.RuntimeDigest = "3a79bf37b571938d1f2907afb6a643f4"
	rec := Record{
		Package:     generated.ObservationProof.Subject.Package,
		Test:        generated.ObservationProof.Subject.Symbol,
		Fingerprint: FromGofresh(generated),
		Outcomes:    map[string]string{"example.com/cacheproof.TestObserved": "passed"},
	}
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	got := Load(dir)
	if len(got) != 1 || got[0].Key() != rec.Key() {
		t.Fatalf("round trip lost the record: %+v", got)
	}

	// A version bump reads as empty — future formats never half-parse.
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	bumped := strings.Replace(string(data), fmt.Sprintf(`"version": %d`, version), fmt.Sprintf(`"version": %d`, version+1), 1)
	if bumped == string(data) {
		t.Fatal("version field not found to bump")
	}
	if err := os.WriteFile(full, []byte(bumped), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("version-mismatched cache loaded %d records", len(got))
	}

	if err := Save(dir, []Record{{Package: rec.Package, Test: rec.Test, Outcomes: map[string]string{rec.Key(): "passed"}}}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("incomplete fingerprint cache loaded %d records", len(got))
	}

	rec.Fingerprint.ResultKind = 0
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("missing result-kind cache loaded %d records", len(got))
	}

	rec.Fingerprint.ResultKind = gofresh.CodeResult
	rec.Fingerprint.RuntimeInputs = "not-base64"
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("malformed runtime manifest cache loaded %d records", len(got))
	}

	rec.Fingerprint.RuntimeInputs = "eyJ2IjoxfQ"
	rec.Outcomes = nil
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("outcomeless cache loaded %d records", len(got))
	}

	rec.Outcomes = map[string]string{rec.Key(): "passed"}
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	withMachine := strings.Replace(string(data), `"resultKind": 1`, `"machine": "", "resultKind": 1`, 1)
	if err := os.WriteFile(full, []byte(withMachine), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("explicit measurement field cache loaded %d records", len(got))
	}

	rec.Outcomes["p.Other"] = "passed"
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("foreign outcome cache loaded %d records", len(got))
	}
	delete(rec.Outcomes, "p.Other")

	rec.Fingerprint.ObservationProof.Symbol = "Other"
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("proof for another subject loaded %d records", len(got))
	}
	rec.Fingerprint.ObservationProof.Symbol = rec.Test

	validEvidence := rec.Fingerprint.ObservationProof.Evidence
	rec.Fingerprint.ObservationProof.Evidence = "proof"
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("malformed proof evidence loaded %d records", len(got))
	}
	rec.Fingerprint.ObservationProof.Evidence = validEvidence

	rec.Fingerprint.ObservationProof.Reason = "blocked"
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("positive proof with a reason loaded %d records", len(got))
	}
	rec.Fingerprint.ObservationProof.Observable = false
	rec.Fingerprint.ObservationProof.Reason = ""
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("negative proof without a reason loaded %d records", len(got))
	}
	rec.Fingerprint.ObservationProof.Observable = true

	withoutObservation := rec
	withoutObservation.Fingerprint.ObservationAssertion = ""
	withoutObservation.Fingerprint.ObservationProof = nil
	if err := Save(dir, []Record{withoutObservation}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	nullAssertion := strings.Replace(string(data), `"runtimeInputs":`, `"observationAssertion": null, "runtimeInputs":`, 1)
	if err := os.WriteFile(full, []byte(nullAssertion), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("null observation assertion loaded %d records", len(got))
	}

	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	nullReason := strings.Replace(string(data), `"observable": true,`, `"observable": true, "reason": null,`, 1)
	if err := os.WriteFile(full, []byte(nullReason), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("null positive-proof reason loaded %d records", len(got))
	}

	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	emptyReason := strings.Replace(string(data), `"observable": true,`, `"observable": true, "reason": "",`, 1)
	if err := os.WriteFile(full, []byte(emptyReason), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("positive proof with explicit empty reason loaded %d records", len(got))
	}

	negative := rec
	negativeProof := *rec.Fingerprint.ObservationProof
	negativeProof.Observable = false
	negativeProof.Reason = "blocked"
	negative.Fingerprint.ObservationProof = &negativeProof
	if err := Save(dir, []Record{negative}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	duplicateObservable := strings.Replace(string(data), `"observable": false,`, `"observable": null, "observable": false,`, 1)
	if err := os.WriteFile(full, []byte(duplicateObservable), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("proof with duplicate observable loaded %d records", len(got))
	}

	if err := Save(dir, []Record{negative}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	absentObservable := strings.Replace(string(data), `"observable": false,`, ``, 1)
	if err := os.WriteFile(full, []byte(absentObservable), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("proof without observable loaded %d records", len(got))
	}

	if err := Save(dir, []Record{negative}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	nullObservable := strings.Replace(string(data), `"observable": false`, `"observable": null`, 1)
	if err := os.WriteFile(full, []byte(nullObservable), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("proof with null observable loaded %d records", len(got))
	}

	rec.Fingerprint.PurityAssertion = "source directive"
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	nullPurity := strings.Replace(string(data), `"purityAssertion": "source directive"`, `"purityAssertion": null`, 1)
	if err := os.WriteFile(full, []byte(nullPurity), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("null purity cache loaded %d records", len(got))
	}

	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	nullRegistrations := strings.Replace(string(data), `"outcomes":`, `"registrations": null, "outcomes":`, 1)
	if err := os.WriteFile(full, []byte(nullRegistrations), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != nil {
		t.Fatalf("null registrations cache loaded %d records", len(got))
	}

	if err := Save(dir, nil); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"records": []`) {
		t.Fatalf("nil records encoded as non-array: %s", data)
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
