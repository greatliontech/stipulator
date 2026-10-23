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

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestWitnessNestedOccurrencesRefuseBeforeAttribution(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-validation", "REQ-evidence-witness-cache-format-proof-and-outcomes", "REQ-evidence-witness-cache-refusals", "REQ-evidence-witness-cache-format-ledger", "REQ-evidence-witness-cache-format-record-keys")
	for _, tc := range []struct {
		name, field, value string
		canonical          bool
	}{
		{"outcome-control", "outcomes", `{"example.com/p.TestA":"passed","example.com/p.TestA/child":"skipped"}`, true},
		{"outcome-duplicate", "outcomes", `{"example.com/p.TestA":"passed","example.com/p.TestA":"passed"}`, false},
		{"outcome-null-duplicate", "outcomes", `{"example.com/p.TestA":null,"example.com/p.TestA":"passed"}`, false},
		{"outcome-null", "outcomes", `{"example.com/p.TestA":null}`, false},
		{"outcome-nonstring", "outcomes", `{"example.com/p.TestA":true}`, false},
		{"registration-control", "registrations", `[{"Package":"example.com/p","Test":"TestA","Requirement":"REQ-example"}]`, true},
		{"registration-empty", "registrations", `[]`, true},
		{"registration-duplicate", "registrations", `[{"Package":"example.com/p","Test":"TestA","Requirement":"REQ-example","Requirement":"REQ-example"}]`, false},
		{"registration-null-duplicate", "registrations", `[{"Package":null,"Package":"example.com/p","Test":"TestA","Requirement":"REQ-example"}]`, false},
		{"registration-null-member", "registrations", `[{"Package":"example.com/p","Test":"TestA","Requirement":null}]`, false},
		{"registration-null-entry", "registrations", `[null]`, false},
		{"registration-alias", "registrations", `[{"Package":"example.com/p","Test":"TestA","requirement":"REQ-example"}]`, false},
		{"namespace-control", "observationNamespaces", `[{"dir":"scratch","pattern":"run-*"}]`, true},
		{"namespace-empty", "observationNamespaces", `[]`, true},
		{"namespace-explicit-empty-strings", "observationNamespaces", `[{"dir":"","pattern":""}]`, true},
		{"namespace-explicit-empty-dir", "observationNamespaces", `[{"dir":"","pattern":"run-*"}]`, true},
		{"namespace-explicit-empty-pattern", "observationNamespaces", `[{"dir":"scratch","pattern":""}]`, true},
		{"namespace-missing-dir", "observationNamespaces", `[{"pattern":"run-*"}]`, false},
		{"namespace-missing-pattern", "observationNamespaces", `[{"dir":"scratch"}]`, false},
		{"namespace-missing-both", "observationNamespaces", `[{}]`, false},
		{"namespace-duplicate", "observationNamespaces", `[{"dir":"scratch","dir":"scratch","pattern":"run-*"}]`, false},
		{"namespace-null-duplicate", "observationNamespaces", `[{"dir":null,"dir":"scratch","pattern":"run-*"}]`, false},
		{"namespace-null-member", "observationNamespaces", `[{"dir":"scratch","pattern":null}]`, false},
		{"namespace-null-entry", "observationNamespaces", `[null]`, false},
		{"namespace-alias", "observationNamespaces", `[{"Dir":"scratch","pattern":"run-*"}]`, false},
		{"exclusion-control", "observationExclusions", `["EXAMPLE_INPUT"]`, true},
		{"exclusion-empty", "observationExclusions", `[]`, true},
		{"exclusion-null-entry", "observationExclusions", `[null]`, false},
	} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/legacy=%t", tc.name, legacy), func(t *testing.T) {
				t.Setenv("XDG_CACHE_HOME", t.TempDir())
				dir := t.TempDir()
				candidate := accountRecord(t)
				candidate.CompartmentLedger = simpleLedger(candidate.Test)
				sibling := accountRecord(t)
				sibling.Group = "sibling"
				if legacy {
					candidate.Fingerprint.RuntimeInputs = "eyJ2IjoyfQ"
				}
				for _, rec := range []Record{candidate, sibling} {
					if err := Install(t.Context(), dir, rec); err != nil {
						t.Fatal(err)
					}
				}
				store, err := open(dir)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(recordBytes(t, candidate), &fields); err != nil {
					t.Fatal(err)
				}
				fields[tc.field] = json.RawMessage(tc.value)
				data, err := json.MarshalIndent(fields, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				name := mustName(t, candidate)
				path := filepath.Join(store.Path(), name)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				ledger := ledgerPath(store.Path(), coordinateOf(candidate).key())
				old := time.Unix(1, 0)
				if err := os.Chtimes(ledger, old, old); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					got := Load(t.Context(), dir)
					want := 1
					if tc.canonical && !legacy {
						want = 2
					}
					if len(got.Records) != want {
						t.Fatalf("nested member occurrences admitted malformed evidence or lost sibling: %+v", got.Records)
					}
					found := false
					for _, rec := range got.Records {
						found = found || rec.Group == sibling.Group
					}
					if !found {
						t.Fatal("valid sibling lost")
					}
					if tc.canonical && !legacy {
						if got.Account != nil {
							t.Fatalf("canonical control refused: %v", got.Account)
						}
					} else {
						cause := stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_ENVELOPE
						if tc.canonical {
							cause = stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_RUNTIME_MANIFEST
						}
						row := requireSingleRefusal(t, got, cause)
						if row.GetFile() != name || row.GetCoordinateOmitted() {
							t.Fatalf("refusal metadata changed: %v", row)
						}
						if !tc.canonical && row.GetCoordinate() != nil {
							t.Fatalf("malformed nested body gained validated attribution: %v", row)
						}
						if tc.canonical && row.GetCoordinate().GetTest() != candidate.Test {
							t.Fatalf("canonical legacy control lost attribution: %v", row)
						}
					}
					if _, err := os.Stat(ledger); err != nil {
						t.Fatalf("weak ledger reference lost: %v", err)
					}
					if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, data) {
						t.Fatalf("inspection changed record bytes: %v", err)
					}
				}
			})
		}
	}
}

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
					candidate := Record{Group: "candidate", Package: "example.com/p", Test: "TestCandidate", Fingerprint: ledgerFingerprint(t, strings.Repeat("a", 32)), CompartmentLedger: simpleLedger("TestCandidate"), Outcomes: map[string]string{"example.com/p.TestCandidate": "passed"}}
					good := Record{Group: "good", Package: "example.com/p", Test: "TestGood", Fingerprint: ledgerFingerprint(t, strings.Repeat("b", 32)), CompartmentLedger: simpleLedger("TestGood"), Outcomes: map[string]string{"example.com/p.TestGood": "passed"}}
					for _, rec := range []Record{candidate, good} {
						if err := Install(t.Context(), dir, rec); err != nil {
							t.Fatal(err)
						}
					}
					if loaded := Load(t.Context(), dir).Records; len(loaded) != 2 {
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
					loaded := Load(t.Context(), dir).Records
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
					if got := Load(t.Context(), dir).Records; len(got) != want {
						t.Fatalf("collection lost valid records: %+v", got)
					}
				})
			}
		}
	}
}

func FuzzWitnessEnvelopeNullOccurrences(f *testing.F) {
	for bits := range 256 {
		mask := byte(bits)
		for _, repeat := range []bool{false, true} {
			f.Add(mask, repeat, false, []byte(" \t\r\n"))
			f.Add(mask, repeat, true, []byte("null"))
			f.Add(mask, repeat, true, []byte{})
		}
	}
	f.Fuzz(func(t *testing.T, mask byte, repeat, upper bool, suffix []byte) {
		stipulate.Covers(t, "REQ-evidence-witness-cache-format-validation", "REQ-evidence-witness-cache-format-ledger")
		rec := Record{Group: "group", Package: "example.com/p", Test: "TestA", Fingerprint: ledgerFingerprint(t, strings.Repeat("a", 32)), Outcomes: map[string]string{"example.com/p.TestA": "passed"}}
		base, err := json.Marshal(entry{Version: version, Group: rec.Group, Package: rec.Package, Test: rec.Test, Fingerprint: rec.Fingerprint, Outcomes: rec.Outcomes})
		if err != nil {
			t.Fatal(err)
		}
		// Higher mask bits mutate nested member occurrences independently of
		// the existing outer null, duplicate and alias grammar.
		outcomeMode := (mask >> 3) & 3
		outcomes := fmt.Sprintf(`{%q:"passed"}`, rec.Key())
		switch outcomeMode {
		case 1:
			outcomes = fmt.Sprintf(`{%q:"passed",%q:"passed"}`, rec.Key(), rec.Key())
		case 2:
			outcomes = fmt.Sprintf(`{%q:null,%q:"passed"}`, rec.Key(), rec.Key())
		case 3:
			outcomes = fmt.Sprintf(`{%q:null}`, rec.Key())
		}
		originalOutcomes, err := json.Marshal(rec.Outcomes)
		if err != nil {
			t.Fatal(err)
		}
		base = bytes.Replace(base, originalOutcomes, []byte(outcomes), 1)
		var prefix strings.Builder
		prefix.WriteByte('{')
		for i, name := range []string{"registrations", "observationExclusions", "observationNamespaces"} {
			isNull := mask&(1<<i) != 0
			value := "[]"
			switch {
			case name == "registrations" && mask&32 != 0:
				value = `[{"Package":"example.com/p","Test":"TestA","Requirement":"REQ-example","Requirement":"REQ-example"}]`
			case name == "observationNamespaces" && mask&64 != 0:
				value = `[{"dir":null,"dir":"scratch","pattern":"run-*"}]`
			case name == "observationExclusions" && mask&128 != 0:
				value = `[null]`
			}
			if isNull {
				value = "null"
			}
			if upper {
				name = strings.ToUpper(name)
			}
			fmt.Fprintf(&prefix, "%q:%s,", name, value)
			if repeat {
				fmt.Fprintf(&prefix, "%q:[],", name)
			}
		}
		data := append(append([]byte(prefix.String()), base[1:]...), suffix...)
		got, ledger, ok := decodeRecord(mustName(t, rec), data)
		whole := len(bytes.Trim(suffix, " \t\r\n")) == 0
		if ok != (whole && mask == 0 && !repeat && !upper) {
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
		// Exercise reviewed-row ordering independently of malformed member
		// occurrences, so an outer refusal cannot mask a row-order defect.
		rowBytes := suffix[:min(len(suffix), 16)]
		if !reviewedRowsMatchOracle(t, rec, []byte{mask & 3, (mask >> 2) & 3}, []byte{(mask >> 4) & 3, (mask >> 6) & 3}, rowBytes) {
			t.Fatalf("reviewed row admission disagrees with ordered-unique oracle: mask=%d rows=%x", mask, rowBytes)
		}
		// Presence is independent of value: explicitly empty string members
		// remain structural evidence, while missing members cannot gain it.
		namespace := []string{`{}`, `{"dir":""}`, `{"pattern":""}`, `{"dir":"","pattern":""}`}[mask&3]
		presenceData := append([]byte(fmt.Sprintf(`{"observationNamespaces":[%s],`, namespace)), recordBytes(t, rec)[1:]...)
		if _, ledger, admitted := decodeRecord(mustName(t, rec), presenceData); admitted != (mask&3 == 3) || ledger != coordinateOf(rec).key() {
			t.Fatalf("namespace member presence changed admission or weak retention: row=%s admitted=%t ledger=%q", namespace, admitted, ledger)
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
