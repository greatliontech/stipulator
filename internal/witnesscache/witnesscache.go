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
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/closure/testvariant"
	"github.com/greatliontech/gofresh/runtimeinput"

	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/verify"
)

// The store lives under the user cache directory, keyed by the corpus
// root's absolute resolved path — never inside the repository:
// fingerprints pin the toolchain and platform, so a committed cache would
// ping-pong across machines, and a repo-local one dies with every fresh
// worktree (REQ-evidence-witness-cache-format).
// Version 9 requires complete binding evidence under a composite ledger
// coordinate. Prior records re-execute; their evidence is never backfilled.
const version = 9

// ledgerVersion is the ledger store's file version.
const ledgerVersion = 2

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
// fingerprint's (REQ-evidence-witness-cache-format); a fingerprint
// Gofresh's encoder refuses names no file
// (REQ-evidence-witness-cache-format-fingerprint).
func fileName(r Record) (string, error) {
	return recordstore.Name([]string{r.Group, r.Package, r.Test}, r.Fingerprint)
}

// Fingerprint is Gofresh's fingerprint in its published record form:
// the record's `fingerprint` member is Gofresh's own encoding (its
// fingerprint-record clause — stipulator's seventeen keys in their
// order, the guards flattened, the proof nested), decoded by Gofresh's
// decoder, which refuses an unknown, duplicated, or null key, a proof
// without its observable, and a record that is not the form's own
// encoding; a record it refuses fails closed to re-execution like any
// field-blind one (REQ-evidence-witness-cache-format-fingerprint).
type Fingerprint = gofresh.Fingerprint

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
	References []string `json:"references,omitzero"`
}

// CompartmentFileHeader is one compartment file's persisted header identity.
type CompartmentFileHeader struct {
	File     string                   `json:"file"`
	Hash     string                   `json:"hash"`
	Embedded bool                     `json:"embedded,omitempty"`
	Bindings *CompartmentFileBindings `json:"bindings,omitempty"`
}

// CompartmentFileBindings preserves syntax-derived file scope evidence.
type CompartmentFileBindings struct {
	Package    string              `json:"package"`
	References []string            `json:"references,omitzero"`
	Imports    []CompartmentImport `json:"imports,omitzero"`
}

// CompartmentImport is an effective local import name and its import path.
type CompartmentImport struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// CompartmentLedger is the record package's persisted test-variant
// declaration ledger: recorded at publish from the same view snapshot the
// fingerprint's compartment hash pinned, and diffed at serve time against
// the current view's ledger so the inert-growth carve-out can classify how
// the compartment moved (REQ-evidence-witness-freshness-carve-out).
type CompartmentLedger struct {
	BindingStrategy string                   `json:"bindingStrategy"`
	BaseFiles       []CompartmentFileHeader  `json:"baseFiles,omitzero"`
	Declarations    []CompartmentDeclaration `json:"declarations,omitzero"`
	FileHeaders     []CompartmentFileHeader  `json:"fileHeaders,omitzero"`
}

// LedgerFromGofresh converts gofresh's ledger to the wire encoding.
func LedgerFromGofresh(ledger gofresh.TestVariantLedger) *CompartmentLedger {
	out := &CompartmentLedger{
		BindingStrategy: ledger.BindingStrategy,
		BaseFiles:       headersFromGofresh(ledger.BaseFiles),
		FileHeaders:     headersFromGofresh(ledger.FileHeaders),
	}
	if ledger.Declarations != nil {
		out.Declarations = make([]CompartmentDeclaration, 0, len(ledger.Declarations))
	}
	for _, declaration := range ledger.Declarations {
		out.Declarations = append(out.Declarations, CompartmentDeclaration{
			File:       declaration.File,
			Kind:       declaration.Kind,
			Name:       declaration.Name,
			Receiver:   declaration.Receiver,
			Hash:       declaration.Hash,
			Package:    declaration.Package,
			References: slices.Clone(declaration.References),
		})
	}
	return out
}

func headersFromGofresh(headers []gofresh.TestVariantFileHeader) []CompartmentFileHeader {
	if headers == nil {
		return nil
	}
	out := make([]CompartmentFileHeader, len(headers))
	for i, h := range headers {
		out[i] = CompartmentFileHeader{File: h.File, Hash: h.Hash, Embedded: h.Embedded}
		if b := h.Bindings; b != nil {
			wire := &CompartmentFileBindings{Package: b.Package, References: slices.Clone(b.References)}
			if b.Imports != nil {
				wire.Imports = make([]CompartmentImport, len(b.Imports))
			}
			for j, imp := range b.Imports {
				wire.Imports[j] = CompartmentImport{Name: imp.Name, Path: imp.Path}
			}
			out[i].Bindings = wire
		}
	}
	return out
}

func headersToGofresh(headers []CompartmentFileHeader) []gofresh.TestVariantFileHeader {
	if headers == nil {
		return nil
	}
	out := make([]gofresh.TestVariantFileHeader, len(headers))
	for i, h := range headers {
		out[i] = gofresh.TestVariantFileHeader{File: h.File, Hash: h.Hash, Embedded: h.Embedded}
		if b := h.Bindings; b != nil {
			native := &testvariant.TestVariantFileBindings{Package: b.Package, References: slices.Clone(b.References)}
			if b.Imports != nil {
				native.Imports = make([]testvariant.TestVariantImport, len(b.Imports))
			}
			for j, imp := range b.Imports {
				native.Imports[j] = testvariant.TestVariantImport{Name: imp.Name, Path: imp.Path}
			}
			out[i].Bindings = native
		}
	}
	return out
}

// ToGofresh converts the wire encoding back to gofresh's ledger type.
func (l *CompartmentLedger) ToGofresh() gofresh.TestVariantLedger {
	out := gofresh.TestVariantLedger{
		BindingStrategy: l.BindingStrategy,
		BaseFiles:       headersToGofresh(l.BaseFiles),
		FileHeaders:     headersToGofresh(l.FileHeaders),
	}
	if l.Declarations != nil {
		out.Declarations = make([]gofresh.TestVariantDeclaration, 0, len(l.Declarations))
	}
	for _, declaration := range l.Declarations {
		out.Declarations = append(out.Declarations, gofresh.TestVariantDeclaration{
			File:       declaration.File,
			Kind:       declaration.Kind,
			Name:       declaration.Name,
			Receiver:   declaration.Receiver,
			Hash:       declaration.Hash,
			Package:    declaration.Package,
			References: slices.Clone(declaration.References),
		})
	}
	return out
}

// Record is one top-level test's cached witness: the fingerprint that
// produced it, every outcome key it owns ("pkg.Test" and "pkg.Test/sub"),
// and its runtime registrations. CompartmentLedger is the
// effective compartment's declaration ledger, persisted under the complete
// ledger coordinate rather than in the record. Install writes it when set;
// Load leaves it nil, and
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
	// ObservationNamespaces is the canonical reviewed scratch namespace
	// set the record's observation was captured under: a read inside one
	// entered no identity, so the evidence proves nothing about such a
	// surface once the policy no longer declares the namespace, and the
	// record serves only while each is still declared. Absent means the
	// capture ran with none.
	ObservationNamespaces []ScratchNamespace `json:"observationNamespaces,omitempty"`
}

// ScratchNamespace is one reviewed in-module run-scratch namespace as
// the record carries it: a module-relative directory and a
// single-component name pattern.
type ScratchNamespace struct {
	Dir     string `json:"dir"`
	Pattern string `json:"pattern"`
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
	// ObservationNamespaces mirrors Record's field; absent in stores
	// written before scratch namespaces existed — the empty capture-time
	// set.
	ObservationNamespaces []ScratchNamespace `json:"observationNamespaces,omitempty"`
}

// Load reads every variant record of the corpus rooted at dir. A missing store
// is an empty cache, and a malformed, wrong-version, or misnamed file is that
// record alone absent — sibling records stay trusted; refusal is per record
// and costs only that record's execution
// (REQ-evidence-witness-cache-format-validation). One identity may return
// several variants: distinct tree states coexist, and serving picks whichever
// fingerprint proves equivalence. Variants come most recently installed first
// (names break ties), so serving's first round tries the variant the last
// state change produced — the one that proves equivalent whenever the tree has
// not alternated since. Ledgers no record file names are reclaimed here: the
// ledger store is bounded by the record store, whose variant bound evicts
// records without reading them.
func Load(ctx context.Context, dir string) []Record {
	return loadSince(ctx, dir, time.Now())
}

// betweenScans, when set, runs after the load's snapshot and before its
// late scan — the window a concurrent install lands in; a test installs
// there to witness that the late scan keeps the landed record's ledger.
var betweenScans func()

// loadSince is Load with the moment the load is taken to begin: a
// ledger no younger than it is a concurrent install's and is spared.
func loadSince(ctx context.Context, dir string, started time.Time) []Record {
	if ctx.Err() != nil {
		return nil
	}
	store, err := open(dir)
	if err != nil {
		return nil
	}
	names, err := store.Names()
	if err != nil {
		return nil
	}
	var records []Record
	referenced := map[string]bool{}
	for _, name := range names {
		if ctx.Err() != nil {
			return nil
		}
		data, _ := store.Read(name)
		rec, digest, ok := loadEntry(name, data, dir)
		if digest != "" {
			referenced[digest] = true
		}
		if ok {
			records = append(records, rec)
		}
	}
	if betweenScans != nil {
		betweenScans()
	}
	// The reference snapshot and reclamation share the install lock. This
	// includes old ledgers reused without a rewrite: age alone cannot protect
	// them while a new referring record is between its reuse check and rename.
	if ctx.Err() != nil {
		return nil
	}
	if lock, err := store.TryLock(); err == nil {
		defer lock.Close()
		if late, err := store.Names(); err == nil {
			for _, name := range late {
				if ctx.Err() != nil {
					return nil
				}
				data, _ := store.Read(name)
				if _, digest, _ := loadEntry(name, data, dir); digest != "" {
					referenced[digest] = true
				}
			}
			if beforeLedgerSweep != nil {
				beforeLedgerSweep()
			}
			sweepLedgers(ctx, store.Path(), referenced, started)
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return records
}

// beforeLedgerSweep observes the protected interval after the last reference
// snapshot and before deletion. Tests start a competing process at this point.
var beforeLedgerSweep func()

// loadEntry reads one variant file: the record when it is valid, and
// the ledger key whenever its complete coordinate parses — a refused
// record's ledger is kept referenced: every refusal
// here is the record's own bytes' (a field that fails the format, a
// manifest that does not decode as canonical Gofresh v2), and keeping
// a refused record's ledger costs nothing, so a refusal costs the
// record's execution and nothing more.
func loadEntry(name string, data []byte, dir string) (Record, string, bool) {
	rec, digest, ok := decodeRecord(name, data)
	if !ok {
		return Record{}, digest, false
	}
	proof := rec.Fingerprint.ObservationProof
	if (proof != (gofresh.ObservationProof{}) && (proof.Subject.Package != rec.Package || proof.Subject.Symbol != rec.Test)) ||
		!validOutcomes(rec) || !validFingerprint(rec.Fingerprint, dir) {
		return Record{}, digest, false
	}
	return rec, digest, true
}

// decodeRecord is the one admission every reader of a variant file
// shares — the loader and the garbage collector alike: the file
// parses whole, is of this version, carries its identity, and is named
// by its content; the ledger key is returned whenever its complete
// coordinate parses, so a refused record's ledger stays referenced. What
// a file passes here and still fails is the record's own shape
// judgment (validFingerprint), never the store's; whether it then
// serves is the engine's currency comparison against the tree.
func decodeRecord(name string, data []byte) (Record, string, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return Record{}, "", false
	}
	digest := ledgerReference(fields)
	if value, ok := fields["registrations"]; ok && isJSONNull(value) {
		return Record{}, digest, false
	}
	var e entry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&e) != nil || dec.Decode(&struct{}{}) != io.EOF || e.Version != version {
		return Record{}, digest, false
	}
	rec := Record{Group: e.Group, Package: e.Package, Test: e.Test, Fingerprint: e.Fingerprint, Outcomes: e.Outcomes, Regs: e.Regs, ObservationExclusions: e.ObservationExclusions, ObservationNamespaces: e.ObservationNamespaces}
	if rec.Group == "" || rec.Package == "" || rec.Test == "" {
		return Record{}, digest, false
	}
	// A decoded fingerprint passed Gofresh's decoder, whose ladder is the
	// encoder's, so it always names a file; a file without the member
	// decodes to the zero fingerprint, whose empty name matches no file.
	want, _ := fileName(rec)
	if name != want {
		return Record{}, digest, false
	}
	return rec, digest, true
}

// sweepLedgers removes every ledger file whose digest no record file
// names, sparing files younger than since — a concurrent install's
// ledger, whose record is about to land; a removal failure costs
// nothing but the file's bytes.
func sweepLedgers(ctx context.Context, store string, referenced map[string]bool, since time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ledgers, err := os.ReadDir(filepath.Join(store, "ledgers"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range ledgers {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if referenced[strings.TrimSuffix(e.Name(), ".json")] {
			continue
		}
		if info, statErr := e.Info(); statErr == nil && !info.ModTime().Before(since) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if rmErr := os.Remove(filepath.Join(store, "ledgers", e.Name())); rmErr != nil && err == nil {
			err = rmErr
		}
	}
	return err
}

// ledgerCoordinate is the complete historical provenance trusted by the
// applicability check. Its ordered JSON encoding is hashed for the file name.
// The binding strategy here selects the recognized format; the ledger must
// independently carry that strategy, never acquire it during a conversion.
type ledgerCoordinate struct {
	Group           string `json:"group"`
	Package         string `json:"package"`
	Core            string `json:"core"`
	Compartment     string `json:"compartment"`
	Toolchain       string `json:"toolchain"`
	BuildConfig     string `json:"buildConfig"`
	ClosureStrategy string `json:"closureStrategy"`
	BindingStrategy string `json:"bindingStrategy"`
}

func coordinateOf(rec Record) ledgerCoordinate {
	f := rec.Fingerprint
	return ledgerCoordinate{rec.Group, rec.Package, f.MaximalClosure,
		f.EffectiveTestVariantClosure(), f.Guards.Toolchain, f.Guards.BuildConfig,
		f.ClosureStrategy, testvariant.BindingStrategy}
}

func (c ledgerCoordinate) key() string {
	if c.Group == "" || c.Package == "" || !ValidDigest(c.Core) || !ValidDigest(c.Compartment) || c.Toolchain == "" || !ValidDigest(c.BuildConfig) || c.ClosureStrategy == "" || c.BindingStrategy != testvariant.BindingStrategy {
		return ""
	}
	data, _ := json.Marshal(c) // strings only
	return recordstore.Digest(string(data))
}

// ledgerReference reads only the coordinate, even when other record fields
// refuse. Load keeps such a file's ledger without granting its outcome.
func ledgerReference(fields map[string]json.RawMessage) string {
	var rec Record
	var f struct {
		MaximalClosure     string                                 `json:"maximalClosure"`
		TestVariantClosure string                                 `json:"testVariantClosure"`
		Toolchain          string                                 `json:"toolchain"`
		BuildConfig        string                                 `json:"buildConfig"`
		ClosureStrategy    string                                 `json:"closureStrategy"`
		Applicability      *gofresh.InertTestVariantApplicability `json:"inertTestVariantApplicability"`
	}
	if json.Unmarshal(fields["group"], &rec.Group) != nil || json.Unmarshal(fields["package"], &rec.Package) != nil || json.Unmarshal(fields["fingerprint"], &f) != nil {
		return ""
	}
	rec.Fingerprint.MaximalClosure = f.MaximalClosure
	rec.Fingerprint.TestVariantClosure = f.TestVariantClosure
	if f.Applicability != nil {
		rec.Fingerprint.InertTestVariantApplicability = *f.Applicability
	}
	rec.Fingerprint.Guards.Toolchain = f.Toolchain
	rec.Fingerprint.Guards.BuildConfig = f.BuildConfig
	rec.Fingerprint.ClosureStrategy = f.ClosureStrategy
	return coordinateOf(rec).key()
}

// ledgerEntry repeats the complete coordinate so a misplaced file refuses.
type ledgerEntry struct {
	Version    int              `json:"version"`
	Coordinate ledgerCoordinate `json:"coordinate"`
	CompartmentLedger
}

func ledgerPath(store, digest string) string {
	return filepath.Join(store, "ledgers", digest+".json")
}

// LoadLedger reads the ledger at the record's complete effective coordinate:
// nil when no ledger is stored, when the file is
// malformed, of another version, or disagrees with its name, or when the
// ledger does not declare test as a receiverless func — a witness's own
// declaration lives in its compartment, so a ledger omitting it would let
// that declaration ride an inert diff as an addition. Refusal costs only
// the carve-out: the record still serves on plain validity.
func LoadLedger(dir string, rec Record) *CompartmentLedger {
	store, err := StoreDir(dir)
	if err != nil {
		return nil
	}
	ledger := readLedger(store, coordinateOf(rec))
	if ledger == nil {
		return nil
	}
	for _, declaration := range ledger.Declarations {
		if declaration.Kind == "func" && declaration.Receiver == "" && declaration.Name == rec.Test {
			return ledger
		}
	}
	return nil
}

// readLedger reads the ledger file under its complete coordinate as far as its own
// structure goes: nil when absent, malformed, of another version,
// disagreeing with its name, or carrying an entry without a file or a
// well-formed digest.
func readLedger(store string, coordinate ledgerCoordinate) *CompartmentLedger {
	digest := coordinate.key()
	if digest == "" {
		return nil
	}
	data, err := os.ReadFile(ledgerPath(store, digest))
	if err != nil {
		return nil
	}
	var e ledgerEntry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&e) != nil || dec.Decode(&struct{}{}) != io.EOF || e.Version != ledgerVersion || e.Coordinate != coordinate || !validLedger(&e.CompartmentLedger) {
		return nil
	}
	// Reject duplicated or null fields without conflating a missing slice
	// with a present empty slice. Whitespace is envelope formatting only.
	canonical, err := json.Marshal(e)
	var compact bytes.Buffer
	if err != nil || json.Compact(&compact, data) != nil || !bytes.Equal(canonical, compact.Bytes()) {
		return nil
	}
	return &e.CompartmentLedger
}

func validLedger(ledger *CompartmentLedger) bool {
	if ledger.BindingStrategy != testvariant.BindingStrategy {
		return false
	}
	for _, declaration := range ledger.Declarations {
		if declaration.File == "" || declaration.Kind == "" || !ValidDigest(declaration.Hash) {
			return false
		}
	}
	files := make(map[string]*CompartmentFileBindings)
	for _, headers := range [][]CompartmentFileHeader{ledger.BaseFiles, ledger.FileHeaders} {
		for _, h := range headers {
			if h.File == "" {
				return false
			}
			if _, duplicate := files[h.File]; duplicate {
				return false
			}
			files[h.File] = h.Bindings
			if (!h.Embedded && h.Bindings == nil) || (h.Bindings != nil && h.Bindings.Package == "") {
				return false
			}
			if h.Bindings != nil {
				for _, imp := range h.Bindings.Imports {
					if imp.Name == "" || imp.Path == "" {
						return false
					}
				}
			}
		}
	}
	for _, h := range ledger.BaseFiles {
		if h.Hash != "" || h.Embedded {
			return false
		}
	}
	for _, h := range ledger.FileHeaders {
		if !ValidDigest(h.Hash) {
			return false
		}
	}
	for _, d := range ledger.Declarations {
		if b := files[d.File]; b == nil || b.Package != d.Package {
			return false
		}
	}
	return true
}

// installLedger persists rec's ledger under its complete coordinate. A
// readable file at that coordinate stays; a torn or prior-version file is
// replaced by the supplied complete ledger before its record can land.
func installLedger(ctx context.Context, store string, rec Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rec.CompartmentLedger == nil {
		return nil
	}
	coordinate := coordinateOf(rec)
	digest := coordinate.key()
	if digest == "" || !validLedger(rec.CompartmentLedger) {
		return fmt.Errorf("witnesscache: incomplete ledger coordinate or binding evidence for %s", rec.Key())
	}
	full := ledgerPath(store, digest)
	if readLedger(store, coordinate) != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ledgerEntry{Version: ledgerVersion, Coordinate: coordinate, CompartmentLedger: *rec.CompartmentLedger}, "", "  ")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
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

// valid is the record's shape judgment — every field the format
// requires, well-formed, the runtime-input manifest decoding as
// canonical Gofresh v1 under the tree root (Describe, whose refusal
// set is the decoder's plus the root's absolute form, which every
// caller's absolute root meets). Whether the manifest's inputs are
// current is gofresh's fingerprint tier's comparison, never a judgment
// here: recomputing the manifest faults on nothing beyond this
// judgment but a cancellation or the environment's normalization — a
// path that cannot be hashed is an unverifiable mark, not an error —
// and this judgment reads no environment, so a duplicate key in the
// ambient environment no longer refuses every record of the store as
// the recomputation once did.
// validFingerprint is this store's own completeness over a decoded
// record: every serving tier present and well-formed, and a code result
// — Gofresh's decoder having judged the form, and its ladder having
// refused a measurement guard on a code result, so the predicate does
// not restate that.
func validFingerprint(f Fingerprint, dir string) bool {
	_, manifestErr := runtimeinput.Describe(f.RuntimeInputs, dir)
	return ValidDigest(f.MaximalClosure) && ValidDigest(f.TestVariantClosure) && ValidDigest(f.EffectiveTestVariantClosure()) && f.Guards.Toolchain != "" && ValidDigest(f.Guards.BuildConfig) &&
		validObservation(f) && validPurity(f.PurityAssertion) && manifestErr == nil && ValidDigest(f.RuntimeDigest) &&
		f.ResultKind == gofresh.CodeResult
}

func validObservation(f Fingerprint) bool {
	absent := f.ObservationProof == (gofresh.ObservationProof{})
	if f.ObservationAssertion == "" && absent {
		return true
	}
	if absent {
		return false
	}
	return f.ObservationAssertion == "caller assertion" &&
		f.ObservationProof.Strategy == gofresh.ObservationRTA &&
		f.ObservationProof.Subject.Package != "" && f.ObservationProof.Subject.Symbol != "" &&
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
func Install(ctx context.Context, dir string, rec Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store, err := open(dir)
	if err != nil {
		return err
	}
	// The name first: a fingerprint Gofresh's encoder refuses installs
	// nothing — not even its compartment's ledger.
	name, err := fileName(rec)
	if err != nil {
		return err
	}
	lock, err := store.Lock(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	// The ledger lands before the record: a record present in the store
	// finds its compartment's ledger present too.
	if err := installLedger(ctx, store.Path(), rec); err != nil {
		return err
	}
	e := entry{Version: version, Group: rec.Group, Package: rec.Package, Test: rec.Test, Fingerprint: rec.Fingerprint, Outcomes: rec.Outcomes, Regs: rec.Regs, ObservationExclusions: rec.ObservationExclusions, ObservationNamespaces: rec.ObservationNamespaces}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	// A torn variant costs only its own record through the per-file
	// refusal leg, and the store's atomic install makes even that
	// window vanish.
	if err := ctx.Err(); err != nil {
		return err
	}
	return store.Install(variantBound, recordstore.Entry{Name: name, Data: data})
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
// will ever serve. Ledgers no kept record's complete coordinate names go
// with their records; the counts are of record variants alone.
func GC(ctx context.Context, dir string, live func(pkg, test string) bool, liveGroup func(group string) bool) (removed, kept int, err error) {
	return gcSince(ctx, dir, live, liveGroup, time.Now())
}

// gcSince is GC with the moment it is taken to begin: as under a load,
// a ledger no younger than it is a concurrent install's and is spared.
func gcSince(ctx context.Context, dir string, live func(pkg, test string) bool, liveGroup func(group string) bool, started time.Time) (removed, kept int, err error) {
	store, err := open(dir)
	if err != nil {
		return 0, 0, err
	}
	lock, err := store.Lock(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer lock.Close()
	referenced := map[string]bool{}
	removed, kept, err = store.Sweep(ctx, func(name string, data []byte) bool {
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
		referenced[coordinateOf(rec).key()] = true
		return true
	})
	if ctx.Err() != nil {
		return removed, kept, ctx.Err()
	}
	if beforeLedgerSweep != nil {
		beforeLedgerSweep()
	}
	if sweepErr := sweepLedgers(ctx, store.Path(), referenced, started); sweepErr != nil && err == nil {
		err = sweepErr
	}
	if cancelErr := ctx.Err(); cancelErr != nil {
		return removed, kept, cancelErr
	}
	return removed, kept, err
}
