package witnesscache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/quick"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestRecordedReviewedRowsRequireCanonicalSets(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-record-keys", "REQ-evidence-witness-cache-format-validation", "REQ-evidence-witness-cache-refusals", "REQ-evidence-witness-cache-format-ledger")
	outside, err := json.Marshal([]string{filepath.Join(t.TempDir(), "absent-reviewed-surface")})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, field, value string
		canonical          bool
	}{
		{"absent", "", "", true},
		{"exclusions-empty", "observationExclusions", `[]`, true},
		{"exclusions-canonical", "observationExclusions", `["a","b","é","界"]`, true},
		{"exclusions-absolute", "observationExclusions", string(outside), true},
		{"exclusions-descending", "observationExclusions", `["b","a"]`, false},
		{"exclusions-duplicate", "observationExclusions", `["a","a"]`, false},
		{"namespaces-empty", "observationNamespaces", `[]`, true},
		{"namespaces-canonical", "observationNamespaces", `[{"dir":"a","pattern":"a-*"},{"dir":"a","pattern":"z-*"},{"dir":"b","pattern":"a-*"}]`, true},
		{"namespaces-dir-descending", "observationNamespaces", `[{"dir":"b","pattern":"a-*"},{"dir":"a","pattern":"z-*"}]`, false},
		{"namespaces-pattern-descending", "observationNamespaces", `[{"dir":"a","pattern":"z-*"},{"dir":"a","pattern":"a-*"}]`, false},
		{"namespaces-duplicate", "observationNamespaces", `[{"dir":"a","pattern":"a-*"},{"dir":"a","pattern":"a-*"}]`, false},
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
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(recordBytes(t, candidate), &fields); err != nil {
					t.Fatal(err)
				}
				if tc.field != "" {
					fields[tc.field] = json.RawMessage(tc.value)
				}
				data, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				name := mustName(t, candidate)
				// Review rows do not participate in the filename. A refusal must
				// therefore be structural, not accidental name disagreement.
				var decoded entry
				if err := json.Unmarshal(data, &decoded); err != nil || decoded.Fingerprint != candidate.Fingerprint {
					t.Fatalf("fixture changed fingerprint: %v", err)
				}
				store, err := open(dir)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(store.Path(), name)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				ledger := ledgerPath(store.Path(), coordinateOf(candidate).key())
				old := time.Unix(1, 0)
				if err := os.Chtimes(ledger, old, old); err != nil {
					t.Fatal(err)
				}
				if _, digest, admitted := decodeRecord(name, data); admitted != tc.canonical || digest != coordinateOf(candidate).key() {
					t.Fatalf("shared GC admission bypassed canonical set or lost weak ledger reference: admitted=%t digest=%q", admitted, digest)
				}
				for range 2 {
					got := Load(t.Context(), dir)
					want := 1
					if tc.canonical && !legacy {
						want = 2
					}
					if len(got.Records) != want {
						t.Fatalf("noncanonical reviewed rows admitted or canonical sibling lost: %+v", got.Records)
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
							t.Fatalf("canonical rows refused: %v", got.Account)
						}
					} else {
						if len(got.Account.GetRefusals()) != 1 {
							t.Fatalf("missing refusal: %v", got.Account)
						}
						if !tc.canonical && got.Account.GetRefusals()[0].GetCoordinate() != nil {
							t.Fatalf("noncanonical reviewed rows gained attribution: %v", got.Account)
						}
						cause := stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_ENVELOPE
						if tc.canonical {
							cause = stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_RUNTIME_MANIFEST
						}
						row := requireSingleRefusal(t, got, cause)
						if row.GetFile() != name || row.GetCoordinateOmitted() {
							t.Fatalf("wrong refusal metadata: %v", row)
						}
						if tc.canonical && row.GetCoordinate().GetTest() != candidate.Test {
							t.Fatalf("canonical legacy attribution lost: %v", row)
						}
					}
					if _, err := os.Stat(ledger); err != nil {
						t.Fatalf("weak ledger reference lost: %v", err)
					}
					if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, data) {
						t.Fatalf("load normalized historical bytes: %v", err)
					}
				}
			})
		}
	}
}

func TestRecordedReviewedRowsAdmissionMatchesOrderedUniqueOracle(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-record-keys", "REQ-evidence-witness-cache-format-validation")
	base := accountRecord(t)
	property := func(exclusions, directories, patterns []uint8) bool {
		return reviewedRowsMatchOracle(t, base, exclusions, directories, patterns)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 300}); err != nil {
		t.Fatal(err)
	}
	// Explicit short positive cases complement a random population rich in
	// duplicates and descending pairs.
	if !property(nil, nil, nil) || !property([]uint8{0, 1, 2}, []uint8{0, 0, 1}, []uint8{0, 1, 0}) {
		t.Fatal("canonical ordered sets refused")
	}
}

func reviewedRowsMatchOracle(t *testing.T, rec Record, exclusions, directories, patterns []byte) bool {
	t.Helper()
	for _, n := range exclusions {
		rec.ObservationExclusions = append(rec.ObservationExclusions, fmt.Sprintf("path-%02d", n%5))
	}
	for i, n := range directories {
		pattern := byte(0)
		if len(patterns) > 0 {
			pattern = patterns[i%len(patterns)]
		}
		rec.ObservationNamespaces = append(rec.ObservationNamespaces, ScratchNamespace{Dir: fmt.Sprintf("dir-%02d", n%5), Pattern: fmt.Sprintf("run-%02d-*", pattern%5)})
	}
	// Independent all-pairs ordering also excludes nonadjacent duplicates;
	// it does not use the production comparator or adjacent-row predicate.
	want := true
	for i, a := range rec.ObservationExclusions {
		for _, b := range rec.ObservationExclusions[i+1:] {
			want = want && a < b
		}
	}
	for i, a := range rec.ObservationNamespaces {
		for _, b := range rec.ObservationNamespaces[i+1:] {
			want = want && (a.Dir < b.Dir || (a.Dir == b.Dir && a.Pattern < b.Pattern))
		}
	}
	_, _, admitted := decodeRecord(mustName(t, rec), recordBytes(t, rec))
	return admitted == want
}
