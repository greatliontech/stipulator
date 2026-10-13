package witnesscache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/stipulate"
)

func TestOptionalRecordArraysRefuseNull(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-validation", "REQ-evidence-witness-cache-format-ledger")
	for _, field := range []string{"registrations", "observationExclusions", "observationNamespaces"} {
		for _, value := range []string{"[]", "null", " \t null \r\n", "null-then-empty"} {
			for _, upper := range []bool{false, true} {
				// Positive controls use the declared wire names. Case aliases
				// exercise the null refusal, not a promise to accept aliases.
				if upper && value == "[]" {
					continue
				}
				t.Run(fmt.Sprintf("%s/%q/upper=%t", field, value, upper), func(t *testing.T) {
					t.Setenv("XDG_CACHE_HOME", t.TempDir())
					dir := t.TempDir()
					candidate := Record{Group: "candidate", Package: "example.com/p", Test: "TestCandidate", Fingerprint: ledgerFingerprint(strings.Repeat("a", 32)), CompartmentLedger: simpleLedger("TestCandidate"), Outcomes: map[string]string{"example.com/p.TestCandidate": "passed"}}
					good := Record{Group: "good", Package: "example.com/p", Test: "TestGood", Fingerprint: ledgerFingerprint(strings.Repeat("b", 32)), CompartmentLedger: simpleLedger("TestGood"), Outcomes: map[string]string{"example.com/p.TestGood": "passed"}}
					for _, rec := range []Record{candidate, good} {
						if err := Install(t.Context(), dir, rec); err != nil {
							t.Fatal(err)
						}
					}
					if loaded := Load(t.Context(), dir); len(loaded) != 2 {
						t.Fatalf("valid omitted fields refused: %+v", loaded)
					}
					root, err := StoreDir(dir)
					if err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(root, mustName(t, candidate))
					original, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if len(original) == 0 || original[0] != '{' || bytes.Contains(original, []byte(`"`+field+`"`)) {
						t.Fatal("fixture must omit the optional field")
					}
					name := field
					if upper {
						name = strings.ToUpper(field)
					}
					encoded := value
					if value == "null-then-empty" {
						encoded = fmt.Sprintf("null,%q:[]", name)
					}
					data := append([]byte(fmt.Sprintf("{%q:%s,", name, encoded)), original[1:]...)
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
					ledger := ledgerPath(root, coordinateOf(candidate).key())
					old := time.Unix(1, 0)
					if err := os.Chtimes(ledger, old, old); err != nil {
						t.Fatal(err)
					}
					want := 1
					if value == "[]" {
						want = 2
					}
					loaded := Load(t.Context(), dir)
					if len(loaded) != want {
						t.Fatalf("loaded %d records, want %d", len(loaded), want)
					}
					foundGood := false
					for _, rec := range loaded {
						if rec.Key() == good.Key() {
							foundGood = true
						}
					}
					if !foundGood {
						t.Fatal("malformed sibling discarded a valid record")
					}
					if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, data) {
						t.Fatalf("inspection rewrote evidence: %v", err)
					}
					if _, err := os.Stat(ledger); err != nil {
						t.Fatalf("refused record lost its referenced ledger: %v", err)
					}
					removed, kept, err := GC(t.Context(), dir, func(string, string) bool { return true }, func(string) bool { return true })
					if err != nil || removed != 2-want || kept != want {
						t.Fatalf("GC removed=%d kept=%d err=%v", removed, kept, err)
					}
					if got := Load(t.Context(), dir); len(got) != want {
						t.Fatalf("collection lost valid records: %+v", got)
					}
				})
			}
		}
	}
}

func FuzzWitnessEnvelopeNullOccurrences(f *testing.F) {
	for mask := byte(0); mask < 8; mask++ {
		for _, repeat := range []bool{false, true} {
			f.Add(mask, repeat, false, []byte(" \t\r\n"))
			f.Add(mask, repeat, true, []byte("null"))
			f.Add(mask, repeat, true, []byte{})
		}
	}
	f.Fuzz(func(t *testing.T, mask byte, repeat, upper bool, suffix []byte) {
		stipulate.Covers(t, "REQ-evidence-witness-cache-format-validation", "REQ-evidence-witness-cache-format-ledger")
		rec := Record{Group: "group", Package: "example.com/p", Test: "TestA", Fingerprint: ledgerFingerprint(strings.Repeat("a", 32)), Outcomes: map[string]string{"example.com/p.TestA": "passed"}}
		base, err := json.Marshal(entry{Version: version, Group: rec.Group, Package: rec.Package, Test: rec.Test, Fingerprint: rec.Fingerprint, Outcomes: rec.Outcomes})
		if err != nil {
			t.Fatal(err)
		}
		var prefix strings.Builder
		prefix.WriteByte('{')
		for i, name := range []string{"registrations", "observationExclusions", "observationNamespaces"} {
			isNull := mask&(1<<i) != 0
			value := "[]"
			if isNull {
				value = "null"
				if upper {
					name = strings.ToUpper(name)
				}
			}
			fmt.Fprintf(&prefix, "%q:%s,", name, value)
			if repeat && isNull {
				fmt.Fprintf(&prefix, "%q:[],", name)
			}
		}
		data := append(append([]byte(prefix.String()), base[1:]...), suffix...)
		got, ledger, ok := decodeRecord(mustName(t, rec), data)
		whole := len(bytes.Trim(suffix, " \t\r\n")) == 0
		if ok != (whole && mask&7 == 0) {
			t.Fatalf("mask=%d repeat=%t upper=%t suffix=%q: admitted=%t", mask, repeat, upper, suffix, ok)
		}
		wantLedger := ""
		if whole {
			wantLedger = coordinateOf(rec).key()
		}
		if ledger != wantLedger {
			t.Fatalf("lost complete ledger coordinate: %q, want %q", ledger, wantLedger)
		}
		if ok && (got.Key() != rec.Key() || got.Fingerprint != rec.Fingerprint || got.Outcomes[rec.Key()] != "passed") {
			t.Fatalf("record changed: %+v", got)
		}
	})
}

func FuzzWitnessEnvelopeMalformedData(f *testing.F) {
	for _, data := range []string{"", "null", "[]", "[1,2]", "true", "0", `"text"`, "{", `{"x":}`, `{"x":1`, `{"x":null}`, `{"group":"g"} null`} {
		f.Add([]byte(data))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		stipulate.Covers(t, "REQ-evidence-witness-cache-format-validation")
		// Any bytes are legal corruption input. A malformed filename cannot
		// admit even a structurally valid object, and parsing must never panic.
		if _, _, ok := decodeRecord("not-a-record.json", data); ok {
			t.Fatal("accepted arbitrary bytes under a non-record filename")
		}
	})
}
