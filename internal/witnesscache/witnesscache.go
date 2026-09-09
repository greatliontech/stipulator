// Package witnesscache is the witness-freshness memoization
// (REQ-evidence-witness-freshness): per top-level test, the gofresh
// fingerprint that produced an outcome set, the outcomes (subtests
// included), and the runtime registrations. The cache is local and
// discardable — never authoritative, never committed: a record serves only
// when its fingerprint checks valid against the current tree, so serving is
// verification by proven equivalence, and absence of proof runs the test.
package witnesscache

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gofresh/runtimeinput"

	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/verify"
)

// The store lives under the user cache directory, keyed by the corpus
// root's absolute resolved path — never inside the repository:
// fingerprints pin the toolchain and platform, so a committed cache would
// ping-pong across machines, and a repo-local one dies with every fresh
// worktree (REQ-evidence-witness-cache-format).
// Bumped from 7 when the compartment ledger left the record for the
// content-addressed ledger store: a prior record's embedded ledger is an
// unknown field, so field-blind prior records fail closed to
// re-execution.
const version = 8

// ledgerVersion is the ledger store's file version.
const ledgerVersion = 1

// variantBound caps how many tree-state variants one test identity
// retains; eviction is by install recency and costs only execution.
const variantBound = 4

// StoreDir is the witness store for the corpus rooted at dir.
func StoreDir(dir string) (string, error) {
	store, err := open(dir)
	if err != nil {
		return "", err
	}
	return store.Path(), nil
}

// open is the witness kind's record store for the corpus rooted at dir.
func open(dir string) (recordstore.Store, error) { return recordstore.Open("witnesses", dir) }

// fileName is a record variant's file: the identity digest over the
// group's coordinate, the package, and the test, joined with the
// fingerprint's (REQ-evidence-witness-cache-format).
func fileName(r Record) string {
	return recordstore.Name([]string{r.Group, r.Package, r.Test}, r.Fingerprint)
}

type observationProof struct {
	Strategy   string `json:"strategy"`
	Package    string `json:"package"`
	Symbol     string `json:"symbol"`
	Observable bool   `json:"observable"`
	Reason     string `json:"reason,omitempty"`
	Evidence   string `json:"evidence"`
}

func (p *observationProof) UnmarshalJSON(data []byte) error {
	type plain observationProof
	fields, err := uniqueObjectFields(data)
	if err != nil {
		return err
	}
	reason, hasReason := fields["reason"]
	if hasReason && isJSONNull(reason) {
		return errors.New("witnesscache: observation proof reason is null")
	}
	observable, ok := fields["observable"]
	if !ok {
		return errors.New("witnesscache: observation proof observable is absent")
	}
	if isJSONNull(observable) {
		return errors.New("witnesscache: observation proof observable is null")
	}
	var decoded plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	if decoded.Observable && hasReason {
		return errors.New("witnesscache: positive observation proof carries reason")
	}
	*p = observationProof(decoded)
	return nil
}

func uniqueObjectFields(data []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("witnesscache: expected JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("witnesscache: expected JSON object field")
		}
		if _, exists := fields[name]; exists {
			return nil, errors.New("witnesscache: duplicate JSON object field")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return fields, nil
}

// Fingerprint is the serialized gofresh fingerprint — the caller owns the
// wire form (gofresh REQ-fresh-fingerprint-data).
type Fingerprint struct {
	MaximalClosure       string            `json:"maximalClosure"`
	TestVariantClosure   string            `json:"testVariantClosure"`
	Toolchain            string            `json:"toolchain"`
	BuildConfig          string            `json:"buildConfig"`
	Machine              string            `json:"machine,omitempty"`
	RuntimeConfig        string            `json:"runtimeConfig,omitempty"`
	ObservationAssertion string            `json:"observationAssertion,omitempty"`
	ObservationProof     *observationProof `json:"observationProof,omitempty"`
	PurityAssertion      string            `json:"purityAssertion,omitempty"`
	DynamicStateVouches  string            `json:"dynamicStateVouches,omitempty"`
	// SingleSubjectDischarges/PackageProcessDischarges are gofresh's
	// attestation-borne discharge audit; DynamicStateStrategy is the
	// shared-dynamic-state derivation the evidence was computed under —
	// a validity field: the engine refuses to serve a record computed
	// under another strategy, and a record persisted before the field
	// reads as the empty strategy and fails closed to re-execution
	// (the clean-break shape, no back-fill).
	SingleSubjectDischarges  string `json:"singleSubjectDischarges,omitempty"`
	PackageProcessDischarges string `json:"packageProcessDischarges,omitempty"`
	DynamicStateStrategy     string `json:"dynamicStateStrategy,omitempty"`
	// ClosureStrategy is the closure identity's derivation the two
	// closure hashes were folded under (gofresh
	// REQ-closure-identity-strategy) — a validity field exactly as
	// DynamicStateStrategy: the engine compares it, so a record persisted
	// before the field reads as the empty strategy and fails closed to
	// re-execution once (the clean-break shape, no back-fill).
	ClosureStrategy string       `json:"closureStrategy,omitempty"`
	RuntimeInputs   string       `json:"runtimeInputs,omitempty"`
	RuntimeDigest   string       `json:"runtimeDigest,omitempty"`
	ResultKind      gofresh.Kind `json:"resultKind"`
}

func (f *Fingerprint) UnmarshalJSON(data []byte) error {
	type plain Fingerprint
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, ok := fields["machine"]; ok {
		return errors.New("witnesscache: code fingerprint carries machine guard field")
	}
	if _, ok := fields["runtimeConfig"]; ok {
		return errors.New("witnesscache: code fingerprint carries runtime guard field")
	}
	if value, ok := fields["observationProof"]; ok && isJSONNull(value) {
		return errors.New("witnesscache: observation proof is null")
	}
	if value, ok := fields["observationAssertion"]; ok && isJSONNull(value) {
		return errors.New("witnesscache: observation assertion is null")
	}
	if value, ok := fields["purityAssertion"]; ok && isJSONNull(value) {
		return errors.New("witnesscache: purity assertion is null")
	}
	var decoded plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	*f = Fingerprint(decoded)
	return nil
}

// ToGofresh converts to the engine's form.
func (f Fingerprint) ToGofresh() gofresh.Fingerprint {
	fp := gofresh.Fingerprint{
		MaximalClosure:     f.MaximalClosure,
		TestVariantClosure: f.TestVariantClosure,
		Guards: guard.Guards{
			Toolchain:     f.Toolchain,
			BuildConfig:   f.BuildConfig,
			Machine:       f.Machine,
			RuntimeConfig: f.RuntimeConfig,
		},
		ObservationAssertion:     f.ObservationAssertion,
		PurityAssertion:          f.PurityAssertion,
		DynamicStateVouches:      f.DynamicStateVouches,
		SingleSubjectDischarges:  f.SingleSubjectDischarges,
		PackageProcessDischarges: f.PackageProcessDischarges,
		DynamicStateStrategy:     f.DynamicStateStrategy,
		ClosureStrategy:          f.ClosureStrategy,
		RuntimeInputs:            f.RuntimeInputs,
		RuntimeDigest:            f.RuntimeDigest,
		ResultKind:               f.ResultKind,
	}
	if f.ObservationProof != nil {
		fp.ObservationProof = gofresh.ObservationProof{
			Strategy:   f.ObservationProof.Strategy,
			Subject:    gofresh.Subject{Package: f.ObservationProof.Package, Symbol: f.ObservationProof.Symbol},
			Observable: f.ObservationProof.Observable,
			Reason:     f.ObservationProof.Reason,
			Evidence:   f.ObservationProof.Evidence,
		}
	}
	return fp
}

// FromGofresh converts from the engine's form.
func FromGofresh(fp gofresh.Fingerprint) Fingerprint {
	f := Fingerprint{
		MaximalClosure:           fp.MaximalClosure,
		TestVariantClosure:       fp.TestVariantClosure,
		Toolchain:                fp.Guards.Toolchain,
		BuildConfig:              fp.Guards.BuildConfig,
		Machine:                  fp.Guards.Machine,
		RuntimeConfig:            fp.Guards.RuntimeConfig,
		ObservationAssertion:     fp.ObservationAssertion,
		PurityAssertion:          fp.PurityAssertion,
		DynamicStateVouches:      fp.DynamicStateVouches,
		SingleSubjectDischarges:  fp.SingleSubjectDischarges,
		PackageProcessDischarges: fp.PackageProcessDischarges,
		DynamicStateStrategy:     fp.DynamicStateStrategy,
		ClosureStrategy:          fp.ClosureStrategy,
		RuntimeInputs:            fp.RuntimeInputs,
		RuntimeDigest:            fp.RuntimeDigest,
		ResultKind:               fp.ResultKind,
	}
	if fp.ObservationProof != (gofresh.ObservationProof{}) {
		f.ObservationProof = &observationProof{
			Strategy:   fp.ObservationProof.Strategy,
			Package:    fp.ObservationProof.Subject.Package,
			Symbol:     fp.ObservationProof.Subject.Symbol,
			Observable: fp.ObservationProof.Observable,
			Reason:     fp.ObservationProof.Reason,
			Evidence:   fp.ObservationProof.Evidence,
		}
	}
	return f
}

// CompartmentDeclaration is one persisted test-variant declaration entry.
type CompartmentDeclaration struct {
	File     string `json:"file"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Receiver string `json:"receiver,omitempty"`
	Hash     string `json:"hash"`
	// Package and References ride the diff identity and the inert
	// classifier's fold soundness (gofresh's compartment ledger); a
	// record persisted without them cannot be diffed faithfully, which
	// is why their introduction bumped the record version - prior
	// versions fail closed to re-execution.
	Package    string   `json:"package,omitempty"`
	References []string `json:"references,omitempty"`
}

// CompartmentFileHeader is one compartment file's persisted header identity.
type CompartmentFileHeader struct {
	File     string `json:"file"`
	Hash     string `json:"hash"`
	Embedded bool   `json:"embedded,omitempty"`
}

// CompartmentLedger is the record package's persisted test-variant
// declaration ledger: recorded at publish from the same view snapshot the
// fingerprint's compartment hash pinned, and diffed at serve time against
// the current view's ledger so the inert-growth carve-out can classify how
// the compartment moved (REQ-evidence-witness-freshness).
type CompartmentLedger struct {
	Declarations []CompartmentDeclaration `json:"declarations,omitempty"`
	FileHeaders  []CompartmentFileHeader  `json:"fileHeaders,omitempty"`
}

// LedgerFromGofresh converts gofresh's ledger to the wire encoding.
func LedgerFromGofresh(ledger gofresh.TestVariantLedger) *CompartmentLedger {
	out := &CompartmentLedger{
		Declarations: make([]CompartmentDeclaration, 0, len(ledger.Declarations)),
		FileHeaders:  make([]CompartmentFileHeader, 0, len(ledger.FileHeaders)),
	}
	for _, declaration := range ledger.Declarations {
		out.Declarations = append(out.Declarations, CompartmentDeclaration{
			File:       declaration.File,
			Kind:       declaration.Kind,
			Name:       declaration.Name,
			Receiver:   declaration.Receiver,
			Hash:       declaration.Hash,
			Package:    declaration.Package,
			References: declaration.References,
		})
	}
	for _, header := range ledger.FileHeaders {
		out.FileHeaders = append(out.FileHeaders, CompartmentFileHeader(header))
	}
	return out
}

// ToGofresh converts the wire encoding back to gofresh's ledger type.
func (l *CompartmentLedger) ToGofresh() gofresh.TestVariantLedger {
	out := gofresh.TestVariantLedger{
		Declarations: make([]gofresh.TestVariantDeclaration, 0, len(l.Declarations)),
		FileHeaders:  make([]gofresh.TestVariantFileHeader, 0, len(l.FileHeaders)),
	}
	for _, declaration := range l.Declarations {
		out.Declarations = append(out.Declarations, gofresh.TestVariantDeclaration{
			File:       declaration.File,
			Kind:       declaration.Kind,
			Name:       declaration.Name,
			Receiver:   declaration.Receiver,
			Hash:       declaration.Hash,
			Package:    declaration.Package,
			References: declaration.References,
		})
	}
	for _, header := range l.FileHeaders {
		out.FileHeaders = append(out.FileHeaders, gofresh.TestVariantFileHeader(header))
	}
	return out
}

// Record is one top-level test's cached witness: the fingerprint that
// produced it, every outcome key it owns ("pkg.Test" and "pkg.Test/sub"),
// and its runtime registrations. CompartmentLedger is the producing
// compartment's declaration ledger, persisted once per compartment in
// the ledger store under the fingerprint's test-variant digest rather
// than in the record: every test of a package shares its compartment,
// so a per-record copy multiplied one ledger by the package's test
// count, and reading them all made loading the store cost more than
// the run it serves. Install writes it when set; Load leaves it nil, and
// LoadLedger reads it back on demand.
type Record struct {
	// Group is the producing capture group's stable digest: the record's
	// identity coordinate across producer environments. A test selected
	// by several eligible invocations holds one record per group, each
	// serving only the environment that produced it.
	Group             string                `json:"group"`
	Package           string                `json:"package"`
	Test              string                `json:"test"`
	Fingerprint       Fingerprint           `json:"fingerprint"`
	CompartmentLedger *CompartmentLedger    `json:"-"`
	Outcomes          map[string]string     `json:"outcomes"`
	Regs              []verify.Registration `json:"registrations,omitempty"`
	// ObservationExclusions is the canonical reviewed exclusion set the
	// record's observation was captured under: every identity here was
	// elided from the manifest, so the evidence proves nothing about
	// those surfaces and serves only while the current policy still
	// asserts each one. Absent means the capture ran with no reviewed
	// exclusions (the built-in pair is tool semantics, never recorded).
	ObservationExclusions []string `json:"observationExclusions,omitempty"`
}

func (r *Record) UnmarshalJSON(data []byte) error {
	type plain Record
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if value, ok := fields["registrations"]; ok && isJSONNull(value) {
		return errors.New("witnesscache: registrations are null")
	}
	var decoded plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	*r = Record(decoded)
	return nil
}

func isJSONNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

// entry is one variant file's content: a versioned single record.
type entry struct {
	Version     int                   `json:"version"`
	Group       string                `json:"group"`
	Package     string                `json:"package"`
	Test        string                `json:"test"`
	Fingerprint Fingerprint           `json:"fingerprint"`
	Outcomes    map[string]string     `json:"outcomes"`
	Regs        []verify.Registration `json:"registrations,omitempty"`
	// ObservationExclusions mirrors Record's field; absent in stores
	// written before reviewed exclusions existed, which is exactly the
	// empty capture-time set.
	ObservationExclusions []string `json:"observationExclusions,omitempty"`
}

// Load reads every variant record of the corpus rooted at dir. A missing
// store is an empty cache, and a malformed, wrong-version, or
// misnamed file is that record alone absent — sibling records stay
// trusted; refusal is per record and costs only that record's execution
// (REQ-evidence-witness-cache-format). One identity may return several
// variants: distinct tree states coexist, and serving picks whichever
// fingerprint proves equivalence. Variants come most recently installed
// first (names break ties), so serving's first round tries the variant
// the last state change produced — the one that proves equivalent
// whenever the tree has not alternated since. Ledgers no record file
// names are reclaimed here: the ledger store is bounded by the record
// store, whose variant bound evicts records without reading them.
func Load(dir string) []Record {
	return loadSince(dir, time.Now())
}

// betweenScans, when set, runs after the load's snapshot and before its
// late scan — the window a concurrent install lands in; a test installs
// there to witness that the late scan keeps the landed record's ledger.
var betweenScans func()

// loadSince is Load with the moment the load is taken to begin: a
// ledger no younger than it is a concurrent install's and is spared.
func loadSince(dir string, started time.Time) []Record {
	store, err := open(dir)
	if err != nil {
		return nil
	}
	// The legacy in-repo cache is never read again; remove it best-effort
	// once per load so migrated corpora stop carrying it.
	os.RemoveAll(filepath.Join(dir, ".stipulator", "cache"))
	names, err := store.Names()
	if err != nil {
		return nil
	}
	manifests := map[string]bool{}
	var records []Record
	referenced := map[string]bool{}
	for _, name := range names {
		data, _ := store.Read(name)
		rec, digest, ok := loadEntry(name, data, dir, manifests)
		if digest != "" {
			referenced[digest] = true
		}
		if ok {
			records = append(records, rec)
		}
	}
	// Records that landed while this load validated its snapshot name
	// ledgers the snapshot never saw: they are read for their digests
	// before the sweep, and a ledger younger than the load is left
	// alone, so a concurrent install's ledger-then-record ordering holds
	// for the sweep as it does for a reader.
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	if betweenScans != nil {
		betweenScans()
	}
	late, _ := store.Names()
	for _, name := range late {
		if seen[name] {
			continue
		}
		data, _ := store.Read(name)
		if _, digest, _ := loadEntry(name, data, dir, manifests); digest != "" {
			referenced[digest] = true
		}
	}
	sweepLedgers(store.Path(), referenced, started)
	return records
}

// loadEntry reads one variant file: the record when it is valid, and
// the compartment digest its fingerprint names whenever the file parses
// at all — a refused record's ledger is kept referenced, so a refusal
// this tree state decides (a manifest not current here) costs the
// record's execution and nothing more.
func loadEntry(name string, data []byte, dir string, manifests map[string]bool) (Record, string, bool) {
	rec, digest, ok := decodeRecord(name, data)
	if !ok {
		return Record{}, digest, false
	}
	proof := rec.Fingerprint.ObservationProof
	if (proof != nil && (proof.Package != rec.Package || proof.Symbol != rec.Test)) ||
		!validOutcomes(rec) || !rec.Fingerprint.valid(dir, manifests) {
		return Record{}, digest, false
	}
	return rec, digest, true
}

// decodeRecord is the one admission every reader of a variant file
// shares — the loader and the garbage collector alike: the file
// parses whole, is of this version, carries its identity, and is named
// by its content; the compartment digest is returned whenever the file
// parses at all, so a refused record's ledger stays referenced. What
// a file passes here and still fails is the tree's judgment (validity
// against the current state), never the store's.
func decodeRecord(name string, data []byte) (Record, string, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return Record{}, "", false
	}
	var named struct {
		TestVariantClosure string `json:"testVariantClosure"`
	}
	_ = json.Unmarshal(fields["fingerprint"], &named)
	digest := named.TestVariantClosure
	if value, ok := fields["registrations"]; ok && isJSONNull(value) {
		return Record{}, digest, false
	}
	var e entry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&e) != nil || dec.Decode(&struct{}{}) != io.EOF || e.Version != version {
		return Record{}, digest, false
	}
	rec := Record{Group: e.Group, Package: e.Package, Test: e.Test, Fingerprint: e.Fingerprint, Outcomes: e.Outcomes, Regs: e.Regs, ObservationExclusions: e.ObservationExclusions}
	if rec.Group == "" || rec.Package == "" || rec.Test == "" || name != fileName(rec) {
		return Record{}, digest, false
	}
	return rec, digest, true
}

// sweepLedgers removes every ledger file whose digest no record file
// names, sparing files younger than since — a concurrent install's
// ledger, whose record is about to land; a removal failure costs
// nothing but the file's bytes.
func sweepLedgers(store string, referenced map[string]bool, since time.Time) error {
	ledgers, err := os.ReadDir(filepath.Join(store, "ledgers"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range ledgers {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if referenced[strings.TrimSuffix(e.Name(), ".json")] {
			continue
		}
		if info, statErr := e.Info(); statErr == nil && !info.ModTime().Before(since) {
			continue
		}
		if rmErr := os.Remove(filepath.Join(store, "ledgers", e.Name())); rmErr != nil && err == nil {
			err = rmErr
		}
	}
	return err
}

// ledgerEntry is one ledger store file: the compartment ledger persisted
// under its test-variant digest, the digest repeated inside so a file
// disagreeing with its own name is refusable on read.
type ledgerEntry struct {
	Version            int                      `json:"version"`
	TestVariantClosure string                   `json:"testVariantClosure"`
	Declarations       []CompartmentDeclaration `json:"declarations,omitempty"`
	FileHeaders        []CompartmentFileHeader  `json:"fileHeaders,omitempty"`
}

func ledgerPath(store, digest string) string {
	return filepath.Join(store, "ledgers", digest+".json")
}

// LoadLedger reads the compartment ledger persisted under digest for the
// record of test: nil when no ledger is stored, when the file is
// malformed, of another version, or disagrees with its name, or when the
// ledger does not declare test as a receiverless func — a witness's own
// declaration lives in its compartment, so a ledger omitting it would let
// that declaration ride an inert diff as an addition. Refusal costs only
// the carve-out: the record still serves on plain validity.
func LoadLedger(dir, digest, test string) *CompartmentLedger {
	store, err := StoreDir(dir)
	if err != nil {
		return nil
	}
	ledger := readLedger(store, digest)
	if ledger == nil {
		return nil
	}
	for _, declaration := range ledger.Declarations {
		if declaration.Kind == "func" && declaration.Receiver == "" && declaration.Name == test {
			return ledger
		}
	}
	return nil
}

// readLedger reads the ledger file under digest as far as its own
// structure goes: nil when absent, malformed, of another version,
// disagreeing with its name, or carrying an entry without a file or a
// well-formed digest.
func readLedger(store, digest string) *CompartmentLedger {
	if !ValidDigest(digest) {
		return nil
	}
	data, err := os.ReadFile(ledgerPath(store, digest))
	if err != nil {
		return nil
	}
	var e ledgerEntry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&e) != nil || dec.Decode(&struct{}{}) != io.EOF || e.Version != ledgerVersion || e.TestVariantClosure != digest {
		return nil
	}
	for _, declaration := range e.Declarations {
		if declaration.File == "" || declaration.Kind == "" || !ValidDigest(declaration.Hash) {
			return nil
		}
	}
	for _, header := range e.FileHeaders {
		if header.File == "" || !ValidDigest(header.Hash) {
			return nil
		}
	}
	return &CompartmentLedger{Declarations: e.Declarations, FileHeaders: e.FileHeaders}
}

// installLedger persists rec's compartment ledger under its test-variant
// digest. The digest addresses the compartment's content, so a file
// present that reads back as a ledger is this ledger and stays; one that
// does not — torn, of a prior version — is rewritten, so a refused file
// never outlives the next install of its compartment.
func installLedger(store string, rec Record) error {
	digest := rec.Fingerprint.TestVariantClosure
	if rec.CompartmentLedger == nil || !ValidDigest(digest) {
		return nil
	}
	full := ledgerPath(store, digest)
	if readLedger(store, digest) != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ledgerEntry{Version: ledgerVersion, TestVariantClosure: digest, Declarations: rec.CompartmentLedger.Declarations, FileHeaders: rec.CompartmentLedger.FileHeaders}, "", "  ")
	if err != nil {
		return err
	}
	return recordstore.WriteAtomic(filepath.Dir(full), ".ledger-*.json", full, data)
}

func validOutcomes(rec Record) bool {
	if rec.Outcomes == nil {
		return false
	}
	if _, ok := rec.Outcomes[rec.Key()]; !ok {
		return false
	}
	for _, outcome := range rec.Outcomes {
		switch outcome {
		case "passed", "failed", "skipped":
		default:
			return false
		}
	}
	prefix := rec.Key() + "/"
	for key := range rec.Outcomes {
		if key != rec.Key() && !strings.HasPrefix(key, prefix) {
			return false
		}
	}
	return true
}

func (f Fingerprint) valid(dir string, manifests map[string]bool) bool {
	validManifest, ok := manifests[f.RuntimeInputs]
	if !ok {
		_, err := runtimeinput.Current(f.RuntimeInputs, dir)
		validManifest = err == nil
		manifests[f.RuntimeInputs] = validManifest
	}
	return ValidDigest(f.MaximalClosure) && ValidDigest(f.TestVariantClosure) && f.Toolchain != "" && ValidDigest(f.BuildConfig) &&
		f.Machine == "" && f.RuntimeConfig == "" &&
		validObservation(f) && validPurity(f.PurityAssertion) && validManifest && ValidDigest(f.RuntimeDigest) &&
		f.ResultKind == gofresh.CodeResult
}

func validObservation(f Fingerprint) bool {
	if f.ObservationAssertion == "" && f.ObservationProof == nil {
		return true
	}
	if f.ObservationProof == nil {
		return false
	}
	return f.ObservationAssertion == "caller assertion" &&
		f.ObservationProof.Strategy == gofresh.ObservationRTA &&
		f.ObservationProof.Package != "" && f.ObservationProof.Symbol != "" &&
		f.ObservationProof.Observable == (f.ObservationProof.Reason == "") &&
		ValidDigest(f.ObservationProof.Evidence)
}

// ValidDigest is a Gofresh-owned 16-byte digest in lowercase hex — the
// form every fingerprint tier and every ledger name carries.
func ValidDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16 && strings.ToLower(value) == value
}

func validPurity(value string) bool {
	switch value {
	case "", "caller assertion", "source directive", "caller assertion and source directive":
		return true
	default:
		return false
	}
}

// Install atomically writes one record's variant file and bounds the
// identity's variant set: beyond variantBound, the least recently
// installed variants are evicted — eviction costs only execution.
func Install(dir string, rec Record) error {
	store, err := open(dir)
	if err != nil {
		return err
	}
	// The ledger lands before the record: a record present in the store
	// finds its compartment's ledger present too.
	if err := installLedger(store.Path(), rec); err != nil {
		return err
	}
	e := entry{Version: version, Group: rec.Group, Package: rec.Package, Test: rec.Test, Fingerprint: rec.Fingerprint, Outcomes: rec.Outcomes, Regs: rec.Regs, ObservationExclusions: rec.ObservationExclusions}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	// A torn variant costs only its own record through the per-file
	// refusal leg, and the store's atomic install makes even that
	// window vanish.
	return store.Install(variantBound, recordstore.Entry{Name: fileName(rec), Data: data})
}

// Key is the record's identity.
func (r Record) Key() string { return r.Package + "." + r.Test }

// IdentityKey is the record's full store identity: the producing
// capture group's digest beside the test key, so one test's records
// under two producer environments never shadow each other.
func (r Record) IdentityKey() string { return r.Group + "\x00" + r.Package + "." + r.Test }

// GC removes this corpus's store entries whose witness identity is not
// live - absent from the caller-supplied obligation universe - plus
// unreadable entries (cost, never evidence). It runs only as an
// explicit verb: an identity absent from THIS tree state may be live
// on another branch, and opportunistic eviction would undo the variant
// store's branch-alternation serving (REQ-evidence-store-gc).
// liveGroup judges record-identity coordinates: nil keeps every
// coordinate (cost cleanup never guesses), non-nil retires coordinates
// no current invocation produces — their records are cost no lookup
// will ever serve. Ledgers no kept record's compartment digest names go
// with their records; the counts are of record variants alone.
func GC(dir string, live func(pkg, test string) bool, liveGroup func(group string) bool) (removed, kept int, err error) {
	return gcSince(dir, live, liveGroup, time.Now())
}

// gcSince is GC with the moment it is taken to begin: as under a load,
// a ledger no younger than it is a concurrent install's and is spared.
func gcSince(dir string, live func(pkg, test string) bool, liveGroup func(group string) bool, started time.Time) (removed, kept int, err error) {
	store, err := open(dir)
	if err != nil {
		return 0, 0, err
	}
	referenced := map[string]bool{}
	removed, kept, err = store.Sweep(func(name string, data []byte) bool {
		rec, _, ok := decodeRecord(name, data)
		if !ok || (liveGroup != nil && !liveGroup(rec.Group)) {
			// What the loader permanently refuses — unreadable,
			// identity-less, of a prior version, field-blind, misnamed —
			// or a retired coordinate no current invocation produces:
			// cost with no servable evidence behind it.
			return false
		}
		if !live(rec.Package, rec.Test) {
			return false
		}
		referenced[rec.Fingerprint.TestVariantClosure] = true
		return true
	})
	if sweepErr := sweepLedgers(store.Path(), referenced, started); sweepErr != nil && err == nil {
		err = sweepErr
	}
	return removed, kept, err
}
