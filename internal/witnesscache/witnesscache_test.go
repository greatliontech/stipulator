package witnesscache

import (
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

	rec := Record{
		Package: "p",
		Test:    "T",
		Fingerprint: Fingerprint{
			MaximalClosure: "00000000000000000000000000000000",
			Toolchain:      "toolchain",
			BuildConfig:    "11111111111111111111111111111111",
			RuntimeInputs:  "eyJ2IjoxfQ",
			RuntimeDigest:  "3a79bf37b571938d1f2907afb6a643f4",
			ResultKind:     gofresh.CodeResult,
		},
		Outcomes: map[string]string{"p.T": "passed"},
	}
	if err := Save(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	got := Load(dir)
	if len(got) != 1 || got[0].Key() != "p.T" {
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

	if err := Save(dir, []Record{{Package: "p", Test: "T", Outcomes: map[string]string{"p.T": "passed"}}}); err != nil {
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

	rec.Outcomes = map[string]string{"p.T": "passed"}
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
	want := gofresh.Fingerprint{
		MaximalClosure: "closure",
		Guards: guard.Guards{
			Toolchain:   "toolchain",
			BuildConfig: "build",
		},
		PurityAssertion: "source directive",
		RuntimeInputs:   "manifest",
		RuntimeDigest:   "digest",
		ResultKind:      gofresh.CodeResult,
	}
	if got := FromGofresh(want).ToGofresh(); !reflect.DeepEqual(got, want) {
		t.Fatalf("fingerprint round trip = %+v, want %+v", got, want)
	}
}
