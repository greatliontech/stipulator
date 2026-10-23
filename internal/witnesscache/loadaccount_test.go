package witnesscache

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/quick"
	"time"
	"unicode/utf8"

	"github.com/greatliontech/gofresh/runtimeinput"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/proto"
)

func accountRecord(t *testing.T) Record {
	t.Helper()
	return Record{Group: "group", Package: "example.com/p", Test: "TestA",
		Fingerprint: ledgerFingerprint(t, strings.Repeat("e", 32)),
		Outcomes:    map[string]string{"example.com/p.TestA": "passed"}}
}

func recordBytes(t *testing.T, rec Record) []byte {
	t.Helper()
	data, err := json.Marshal(entry{Version: version, Group: rec.Group, Package: rec.Package, Test: rec.Test,
		Fingerprint: rec.Fingerprint, Outcomes: rec.Outcomes, Regs: rec.Regs,
		ObservationExclusions: rec.ObservationExclusions, ObservationNamespaces: rec.ObservationNamespaces})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func requireSingleRefusal(t *testing.T, result LoadResult, cause stipulatorv1.WitnessCacheRefusalCause) *stipulatorv1.WitnessCacheRefusal {
	t.Helper()
	a := result.Account
	if len(a.GetRefusals()) != 1 || a.GetOmitted() != 0 || a.GetIncomplete() || a.GetProblem() != "" {
		t.Fatalf("expected one complete refusal, got %v", a)
	}
	row := a.GetRefusals()[0]
	if row.GetCause() != cause || row.GetDetail() == "" || row.GetFile() == "" {
		t.Fatalf("missing operation-derived refusal: %v", row)
	}
	return row
}

func TestLoadAccountRuntimeRefusalsPreserveSiblingAndHistory(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals", "REQ-evidence-witness-cache-format-validation")
	for _, manifest := range []string{"eyJ2IjoxfQ", "eyJ2IjoyfQ", "eyJ2IjozfQ", "not-base64", base64.RawURLEncoding.EncodeToString([]byte(`{"v":3,"unknown":true}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"v":4,"unknown":true}`))} {
		t.Run(manifest, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			dir := t.TempDir()
			good := accountRecord(t)
			decoded, err := base64.RawURLEncoding.DecodeString(good.Fingerprint.RuntimeInputs)
			if err != nil || !bytes.HasPrefix(decoded, []byte(`{"v":4`)) {
				t.Fatalf("positive control is not a native v4 manifest: %s, %v", decoded, err)
			}
			if err := Install(t.Context(), dir, good); err != nil {
				t.Fatal(err)
			}
			if clean := Load(t.Context(), dir); len(clean.Records) != 1 || clean.Account != nil {
				t.Fatalf("valid current control failed: %+v", clean)
			}
			bad := good
			bad.Fingerprint.RuntimeInputs = manifest
			bad.Outcomes = map[string]string{bad.Key(): "failed", bad.Key() + "/secret": "passed"}
			bad.Regs = []verify.Registration{{Package: bad.Package, Test: bad.Test, Requirement: "secret"}}
			if err := Install(t.Context(), dir, bad); err != nil {
				t.Fatal(err)
			}
			store, err := open(dir)
			if err != nil {
				t.Fatal(err)
			}
			name := mustName(t, bad)
			before, err := store.Read(name)
			if err != nil {
				t.Fatal(err)
			}
			if rec, _, ok := decodeRecord(name, before); !ok || rec.Fingerprint != bad.Fingerprint {
				t.Fatal("refusal fixture failed outer/native codec control")
			}
			_, nativeErr := runtimeinput.Describe(manifest, dir)
			if nativeErr == nil {
				t.Fatal("negative manifest control unexpectedly supported")
			}
			for range 2 {
				loaded := Load(t.Context(), dir)
				if len(loaded.Records) != 1 || !reflect.DeepEqual(loaded.Records[0], good) {
					t.Fatalf("refused evidence leaked or valid same-identity sibling lost: %+v", loaded.Records)
				}
				row := requireSingleRefusal(t, loaded, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_RUNTIME_MANIFEST)
				c := row.GetCoordinate()
				if row.GetFile() != name || c.GetGroup() != bad.Group || c.GetPackage() != bad.Package || c.GetTest() != bad.Test || row.GetCoordinateOmitted() {
					t.Fatalf("lost validated attribution: %v", row)
				}
				if row.GetDetail() != nativeErr.Error() || row.GetDetailTruncated() {
					t.Fatalf("native diagnostic was reclassified or lost: %v", row)
				}
			}
			after, err := store.Read(name)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("restart changed refused evidence: %v", err)
			}
		})
	}
}

func TestLoadAccountEnvelopeAndEvidenceNeverAttribute(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals", "REQ-evidence-witness-cache-format-validation", "REQ-evidence-witness-cache-format-ledger")
	base := accountRecord(t)
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
		cause  stipulatorv1.WitnessCacheRefusalCause
		ledger bool
	}{
		{"malformed", func([]byte) []byte { return []byte(`{"group":"group",`) }, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_ENVELOPE, false},
		{"duplicate-group", func(b []byte) []byte { return append([]byte(`{"group":"group",`), b[1:]...) }, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_ENVELOPE, true},
		{"case-alias", func(b []byte) []byte { return bytes.Replace(b, []byte(`"test":`), []byte(`"TEST":`), 1) }, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_ENVELOPE, true},
		{"unknown-version", func(b []byte) []byte { return bytes.Replace(b, []byte(`"version":9`), []byte(`"version":10`), 1) }, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_ENVELOPE, true},
		{"missing-outcomes", func(b []byte) []byte {
			var m map[string]json.RawMessage
			json.Unmarshal(b, &m)
			delete(m, "outcomes")
			b, _ = json.Marshal(m)
			return b
		}, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_ENVELOPE, true},
		{"bad-native-codec", func(b []byte) []byte { return bytes.Replace(b, []byte(`"resultKind":1`), []byte(`"resultKind":0`), 1) }, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_EVIDENCE_STRUCTURE, true},
		{"foreign-outcome", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"outcomes":{`), []byte(`"outcomes":{"other.Test":"passed",`), 1)
		}, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_EVIDENCE_STRUCTURE, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			dir := t.TempDir()
			rec := base
			rec.CompartmentLedger = simpleLedger(rec.Test)
			if err := Install(t.Context(), dir, rec); err != nil {
				t.Fatal(err)
			}
			store, err := open(dir)
			if err != nil {
				t.Fatal(err)
			}
			data := tc.mutate(recordBytes(t, rec))
			if bytes.Equal(data, recordBytes(t, rec)) {
				t.Fatal("mutation did not change fixture")
			}
			if err := os.WriteFile(filepath.Join(store.Path(), mustName(t, rec)), data, 0600); err != nil {
				t.Fatal(err)
			}
			ledger := ledgerPath(store.Path(), coordinateOf(rec).key())
			old := time.Unix(1, 0)
			if err := os.Chtimes(ledger, old, old); err != nil {
				t.Fatal(err)
			}
			got := Load(t.Context(), dir)
			if len(got.Records) != 0 {
				t.Fatalf("refused record admitted: %+v", got.Records)
			}
			row := requireSingleRefusal(t, got, tc.cause)
			if row.GetCoordinate() != nil || row.GetCoordinateOmitted() {
				t.Fatalf("unvalidated identity attributed: %v", row)
			}
			_, err = os.Stat(ledger)
			if tc.ledger && err != nil {
				t.Fatalf("weaker ledger reference lost: %v", err)
			}
			if !tc.ledger && !os.IsNotExist(err) {
				t.Fatalf("malformed record retained unreadable reference: %v", err)
			}
		})
	}
	// Complete canonical structures still cannot attribute a misnamed file.
	a := loadEntry("wrong.json", recordBytes(t, base), t.TempDir())
	if a.record != nil || a.refusal.GetCoordinate() != nil || a.refusal.GetCause() != stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_ENVELOPE {
		t.Fatalf("misnamed record established authority: %+v", a)
	}
}

type readFunc func(string) ([]byte, error)

func (f readFunc) Read(name string) ([]byte, error) { return f(name) }

func TestLoadAccountReadFailureAndDiagnosticBounds(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals")
	for _, tc := range []struct {
		value, want string
		truncated   bool
	}{
		{strings.Repeat("x", 1024), strings.Repeat("x", 1024), false},
		{strings.Repeat("x", 1025), strings.Repeat("x", 1024), true},
		{strings.Repeat("界", 341), strings.Repeat("界", 341), false},
		{strings.Repeat("界", 342), strings.Repeat("界", 341), true},
		{strings.Repeat("x", 1021) + "界", strings.Repeat("x", 1021) + "界", false},
		{strings.Repeat("x", 1022) + "界", strings.Repeat("x", 1022), true},
		{"bad\xff.json", `bad\xff.json`, false},
	} {
		got := scanRecords(t.Context(), t.TempDir(), readFunc(func(name string) ([]byte, error) {
			if name != tc.value {
				t.Fatalf("lookup used diagnostic spelling: %q", name)
			}
			return nil, errors.New(tc.value)
		}), []string{tc.value}, map[string]bool{})
		row := requireSingleRefusal(t, got, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_READ)
		if row.GetFile() != tc.want || row.GetDetail() != tc.want || row.GetFilenameTruncated() != tc.truncated || row.GetDetailTruncated() != tc.truncated || !utf8.ValidString(row.GetFile()) || len(row.GetFile()) > 1024 {
			t.Fatalf("bad bounded diagnostic for %q: %v", tc.value, row)
		}
		if len(got.Records) != 0 || row.GetCoordinate() != nil {
			t.Fatal("read failure gained evidence or attribution")
		}
	}
}

func TestLoadAccountCoordinateBoundOmitsWholeIdentity(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals")
	for _, field := range []string{"group", "package", "test"} {
		for _, size := range []int{1024, 1025} {
			rec := accountRecord(t)
			value := strings.Repeat("界", 341) + strings.Repeat("x", size-1023)
			switch field {
			case "group":
				rec.Group = value
			case "package":
				rec.Package = value
			case "test":
				rec.Test = value
			}
			rec.Outcomes = map[string]string{rec.Key(): "passed"}
			rec.Fingerprint.RuntimeInputs = "eyJ2IjoyfQ"
			name := mustName(t, rec)
			got := scanRecords(t.Context(), t.TempDir(), readFunc(func(string) ([]byte, error) { return recordBytes(t, rec), nil }), []string{name}, map[string]bool{})
			row := requireSingleRefusal(t, got, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_RUNTIME_MANIFEST)
			if size > 1024 {
				if !row.GetCoordinateOmitted() || row.GetCoordinate() != nil {
					t.Fatalf("oversized %s became a lookup coordinate: %v", field, row)
				}
			} else {
				c := row.GetCoordinate()
				if row.GetCoordinateOmitted() || c.GetGroup() != rec.Group || c.GetPackage() != rec.Package || c.GetTest() != rec.Test {
					t.Fatalf("boundary identity lost: %v", row)
				}
			}
		}
	}
}

func TestLoadAccountStructurePrecedesRuntimeAttribution(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals", "REQ-evidence-witness-cache-format-validation")
	for _, change := range []func(*Record){
		func(r *Record) { r.Fingerprint.MaximalClosure = "not-a-digest" },
		func(r *Record) { r.Fingerprint.RuntimeDigest = "not-a-digest" },
		func(r *Record) { r.Outcomes = map[string]string{r.Key() + "/child": "passed"} },
	} {
		rec := accountRecord(t)
		rec.Fingerprint.RuntimeInputs = "eyJ2IjoyfQ"
		change(&rec)
		name := mustName(t, rec)
		data := recordBytes(t, rec)
		if _, _, ok := decodeRecord(name, data); !ok {
			t.Fatal("fixture must have a canonical native fingerprint and matching filename")
		}
		got := loadEntry(name, data, t.TempDir())
		if got.record != nil || got.refusal.GetCause() != stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_EVIDENCE_STRUCTURE || got.refusal.GetCoordinate() != nil || got.refusal.GetCoordinateOmitted() {
			t.Fatalf("runtime refusal acquired an invalid structure's identity: %+v", got)
		}
	}
}

func TestLoadAccountStreamingBoundAndCount(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals")
	// Every prefix must be bounded, including prefixes before enumeration ends.
	property := func(n uint8) bool {
		var result LoadResult
		for i := 0; i < int(n); i++ {
			result.refuse(refusal(fmt.Sprintf("%03d.json", i), stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_READ, "unreadable", nil))
			a := result.Account
			if len(a.GetRefusals()) != min(i+1, 25) || a.GetOmitted() != uint64(max(i+1-25, 0)) {
				return false
			}
			for j, row := range a.GetRefusals() {
				if row.GetFile() != fmt.Sprintf("%03d.json", j) {
					return false
				}
			}
		}
		return true
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
	var result LoadResult
	for range 25 {
		result.refuse(refusal("record.json", stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_READ, "unreadable", nil))
	}
	result.Account.SetOmitted(math.MaxUint64)
	result.refuse(refusal("overflow.json", stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_READ, "unreadable", nil))
	if result.Account.GetOmitted() != math.MaxUint64 {
		t.Fatal("omission counter wrapped")
	}
}

func TestLoadAccountSnapshotOrderingCapAndLateLedger(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals", "REQ-evidence-witness-cache-format-ledger")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	good := accountRecord(t)
	if err := Install(t.Context(), dir, good); err != nil {
		t.Fatal(err)
	}
	store, err := open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var refusedNames []string
	for i := range 26 {
		// Seed a pre-existing oversized store directly so per-install variant
		// eviction cannot hide the loader's independent accounting bound.
		bad := good
		bad.Fingerprint.RuntimeInputs = fmt.Sprintf("unsupported-%02d", i)
		name := mustName(t, bad)
		refusedNames = append(refusedNames, name)
		path := filepath.Join(store.Path(), name)
		if err := os.WriteFile(path, recordBytes(t, bad), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	late := good
	late.Group = "late"
	late.Fingerprint.RuntimeInputs = "eyJ2IjoyfQ"
	late.CompartmentLedger = simpleLedger(late.Test)
	betweenScans = func() {
		if err := Install(t.Context(), dir, late); err != nil {
			t.Fatal(err)
		}
		old := time.Unix(1, 0)
		if err := os.Chtimes(ledgerPath(store.Path(), coordinateOf(late).key()), old, old); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { betweenScans = nil })
	got := Load(t.Context(), dir)
	betweenScans = nil
	if len(got.Records) != 1 || !reflect.DeepEqual(got.Records[0], good) {
		t.Fatalf("valid sibling lost: %+v", got.Records)
	}
	a := got.Account
	if len(a.GetRefusals()) != 25 || a.GetOmitted() != 1 || a.GetIncomplete() {
		t.Fatalf("initial snapshot double-counted or late install counted: %v", a)
	}
	for i, row := range a.GetRefusals() {
		if row.GetFile() != refusedNames[25-i] || row.GetCoordinate().GetGroup() != good.Group || row.GetCoordinate().GetTest() != good.Test {
			t.Fatalf("refusal order is not variant order: %v", row)
		}
	}
	if LoadLedger(dir, late) == nil {
		t.Fatal("late refused file lost its old ledger")
	}
	// A later load observes the installed file once, and owns a fresh account.
	before := proto.Clone(a)
	next := Load(t.Context(), dir)
	if len(next.Account.GetRefusals()) != 25 || next.Account.GetOmitted() != 2 || next.Account.GetRefusals()[0].GetFile() != mustName(t, late) {
		t.Fatalf("restart lost a refusal: %v", next.Account)
	}
	next.Account.GetRefusals()[0].SetDetail("changed by projection")
	if !proto.Equal(a, before) {
		t.Fatal("a subsequent load or projection changed the earlier account")
	}
}

func TestLoadAccountDiscoveryAndCancellation(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	if got := Load(t.Context(), dir); got.Account != nil || len(got.Records) != 0 {
		t.Fatalf("missing store is not clean empty: %+v", got)
	}
	store, err := open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	checkIncomplete := func(got LoadResult, operation string) {
		t.Helper()
		a := got.Account
		if len(got.Records) != 0 || !a.GetIncomplete() || len(a.GetRefusals()) != 0 || a.GetOmitted() != 0 || !strings.Contains(a.GetProblem(), operation) || len(a.GetProblem()) > 1024 || !utf8.ValidString(a.GetProblem()) {
			t.Fatalf("pre-record failure invented a row or hid its diagnostic: %+v", got)
		}
	}
	checkIncomplete(Load(t.Context(), dir), "list witness records")
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		if err := os.Remove(store.Path()); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(store.Path(), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(store.Path(), 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(store.Path(), 0700) })
		checkIncomplete(Load(t.Context(), dir), "list witness records")
		if err := os.Chmod(store.Path(), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checkIncomplete(Load(ctx, dir), context.Canceled.Error())
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" && runtime.GOOS != "plan9" {
		t.Setenv("XDG_CACHE_HOME", "")
		t.Setenv("HOME", "")
		checkIncomplete(Load(t.Context(), dir), "discover witness store")
	}
	var got LoadResult
	got = got.incomplete("list witness records", errors.New(strings.Repeat("界", 1000)))
	if !got.Account.GetProblemTruncated() || len(got.Account.GetProblem()) > 1024 || !utf8.ValidString(got.Account.GetProblem()) {
		t.Fatal("account-level problem is not bounded")
	}
}

func TestLoadAccountCancellationCountsOnlyExaminedFiles(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals")
	rec := accountRecord(t)
	for _, cancelAfter := range []int{0, 1, 2, 3} {
		ctx, cancel := context.WithCancel(t.Context())
		reads := 0
		if cancelAfter == 0 {
			cancel()
		}
		reader := readFunc(func(name string) ([]byte, error) {
			reads++
			if reads == cancelAfter {
				cancel()
			}
			if reads == 1 {
				return recordBytes(t, rec), nil
			}
			return nil, errors.New("read denied")
		})
		got := scanRecords(ctx, t.TempDir(), reader, []string{mustName(t, rec), "second.json", "third.json", "unexamined.json"}, map[string]bool{})
		cancel()
		a := got.Account
		if reads != cancelAfter || got.Records != nil || !a.GetIncomplete() || a.GetOmitted() != 0 || len(a.GetRefusals()) != max(cancelAfter-1, 0) || !strings.Contains(a.GetProblem(), context.Canceled.Error()) {
			t.Fatalf("cancellation after %d reads leaked partial evidence or guessed unseen refusals: reads=%d result=%+v", cancelAfter, reads, got)
		}
	}
}

func TestLoadAccountRawFilenameReadFailure(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals")
	if runtime.GOOS == "windows" {
		t.Skip("Unix raw filenames and symlink semantics")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Path(), 0700); err != nil {
		t.Fatal(err)
	}
	name := "bad\xff.json"
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(store.Path(), name)); err != nil {
		t.Fatal(err)
	}
	got := Load(t.Context(), dir)
	row := requireSingleRefusal(t, got, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_READ)
	if row.GetFile() != `bad\xff.json` || row.GetCoordinate() != nil || !utf8.ValidString(row.GetDetail()) {
		t.Fatalf("raw filename became invalid or authoritative: %v", row)
	}
}

func TestLoadAccountCancellationDuringRetentionPreservesObservedCount(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals")
	for _, phase := range []string{"between scans", "before sweep"} {
		t.Run(phase, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			dir := t.TempDir()
			good := accountRecord(t)
			bad := good
			bad.Fingerprint.RuntimeInputs = "eyJ2IjoyfQ"
			for _, rec := range []Record{good, bad} {
				if err := Install(t.Context(), dir, rec); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if phase == "between scans" {
				betweenScans = cancel
			} else {
				beforeLedgerSweep = cancel
			}
			t.Cleanup(func() { betweenScans = nil; beforeLedgerSweep = nil })
			got := Load(ctx, dir)
			if got.Records != nil || !got.Account.GetIncomplete() || len(got.Account.GetRefusals()) != 1 || got.Account.GetOmitted() != 0 || !strings.Contains(got.Account.GetProblem(), context.Canceled.Error()) {
				t.Fatalf("retention cancellation lost count or returned admitted partial evidence: %+v", got)
			}
		})
	}
}
