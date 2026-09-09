// Package resolutioncache is the machine-local store of binding
// resolutions served by proven equivalence: one record per (build
// selection, symbol) carrying the resolution's every served field beside
// the Gofresh fingerprint of the symbol's source closure, served exactly
// when that fingerprint checks valid against the current tree
// (REQ-evidence-resolution-freshness). The layout mirrors the witness
// cache's (REQ-evidence-resolution-cache-format): the store is keyed by
// the corpus root, records are versioned, and a field-blind or
// prior-version record is ignored rather than read.
package resolutioncache

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// version is the record format: bumped when a persisted field is added
// that the serving decision consumes, or when the classifier's contract
// changes what a record proves — either way a prior record fails closed
// to a typed resolution.
const version = 1

// Record is one served resolution: the symbol under its resolving build
// selection, the fingerprint the serve is proven by, and every field a
// verification reads from a resolution.
type Record struct {
	// Selection is the resolving build selection's key — the effective
	// tag set and toolchain the symbol resolved under — so a record
	// serves only the view that produced it (REQ-go-build-selections).
	Selection string
	Symbol    string
	// Fingerprint is the Gofresh fingerprint of the symbol as a subject:
	// source-closure tiers only, never an observation.
	Fingerprint witnesscache.Fingerprint
	// Resolution is "resolved" or "generated_file"; an unresolved symbol
	// has no subject to fingerprint and is never recorded.
	Resolution string
	Shape      string
	Package    string
	// WitnessClass is "proof", "property", or "example" with its reason;
	// NeverServe is the serving refusal a random-seeded or unclassifiable
	// witness carries, empty when serving is allowed.
	WitnessClass       string
	WitnessClassReason string
	NeverServe         string
}

// Key is the record's identity within the store.
func (r Record) Key() string { return r.Selection + "\x00" + r.Symbol }

type entry struct {
	Version            int                      `json:"version"`
	Selection          string                   `json:"selection"`
	Symbol             string                   `json:"symbol"`
	Fingerprint        witnesscache.Fingerprint `json:"fingerprint"`
	Resolution         string                   `json:"resolution"`
	Shape              string                   `json:"shape,omitempty"`
	Package            string                   `json:"package,omitempty"`
	WitnessClass       string                   `json:"witnessClass,omitempty"`
	WitnessClassReason string                   `json:"witnessClassReason,omitempty"`
	NeverServe         string                   `json:"neverServe,omitempty"`
}

// StoreDir is the store's root for the corpus rooted at dir: beside the
// witness cache, keyed by the root's resolved absolute path.
func StoreDir(dir string) (string, error) {
	store, err := open(dir)
	if err != nil {
		return "", err
	}
	return store.Path(), nil
}

// open is the resolution kind's record store for the corpus rooted at dir.
func open(dir string) (recordstore.Store, error) { return recordstore.Open("resolutions", dir) }

// fileName is a record's file: the identity digest over the selection
// key and the symbol, joined with the fingerprint's
// (REQ-evidence-resolution-cache-format).
func fileName(r Record) string {
	return recordstore.Name([]string{r.Selection, r.Symbol}, r.Fingerprint)
}

// Load reads every record of the corpus rooted at dir, most recently
// installed first; a missing store is empty, and a malformed,
// field-blind, prior-version, misnamed, or observation-carrying record
// is skipped.
func Load(dir string) []Record {
	store, err := open(dir)
	if err != nil {
		return nil
	}
	names, err := store.Names()
	if err != nil {
		return nil
	}
	var out []Record
	for _, name := range names {
		data, _ := store.Read(name)
		if rec, ok := decodeRecord(name, data); ok {
			out = append(out, rec)
		}
	}
	return out
}

// decodeRecord is the one admission every reader of a record file
// shares — the loader and the garbage collector alike: the file parses
// whole, is of this version, carries every field this store serves and
// none it does not, and is named by its content.
func decodeRecord(name string, data []byte) (Record, bool) {
	var e entry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&e) != nil || e.Version != version || e.Selection == "" || e.Symbol == "" || !validResolution(e.Resolution) || !validFingerprint(e.Fingerprint) {
		return Record{}, false
	}
	rec := Record{
		Selection: e.Selection, Symbol: e.Symbol, Fingerprint: e.Fingerprint,
		Resolution: e.Resolution, Shape: e.Shape, Package: e.Package,
		WitnessClass: e.WitnessClass, WitnessClassReason: e.WitnessClassReason, NeverServe: e.NeverServe,
	}
	if name != fileName(rec) {
		return Record{}, false
	}
	return rec, true
}

func validResolution(r string) bool { return r == "resolved" || r == "generated_file" }

// validFingerprint admits source-closure tiers only — the maximal
// closure, the test-variant compartment, the toolchain, the build
// configuration, all present: a resolution observes nothing, so any
// observation, purity, or runtime tier marks a record this store did
// not write.
func validFingerprint(f witnesscache.Fingerprint) bool {
	return witnesscache.ValidDigest(f.MaximalClosure) && witnesscache.ValidDigest(f.TestVariantClosure) && f.Toolchain != "" && witnesscache.ValidDigest(f.BuildConfig) &&
		f.ResultKind == gofresh.CodeResult &&
		f.Machine == "" && f.RuntimeConfig == "" && f.ObservationAssertion == "" && f.ObservationProof == nil &&
		f.PurityAssertion == "" && f.DynamicStateVouches == "" && f.RuntimeInputs == "" && f.RuntimeDigest == ""
}

// Install atomically writes one record; a record for the same selection
// and symbol under another fingerprint is replaced, so the store holds
// one record per identity.
func Install(dir string, rec Record) error { return InstallAll(dir, []Record{rec}) }

// InstallAll installs a batch of records under the store's one-record
// retention: the cold publish over a corpus writes hundreds, and the
// store's one scan supersedes every prior fingerprint of a written
// identity. A record carrying a field this store does not serve refuses
// the whole batch before anything is written.
func InstallAll(dir string, recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	store, err := open(dir)
	if err != nil {
		return err
	}
	entries := make([]recordstore.Entry, 0, len(recs))
	for _, rec := range recs {
		if !validResolution(rec.Resolution) || !validFingerprint(rec.Fingerprint) || rec.Selection == "" || rec.Symbol == "" {
			return errors.New("resolutioncache: record carries a field this store does not serve")
		}
		e := entry{Version: version, Selection: rec.Selection, Symbol: rec.Symbol, Fingerprint: rec.Fingerprint, Resolution: rec.Resolution, Shape: rec.Shape, Package: rec.Package, WitnessClass: rec.WitnessClass, WitnessClassReason: rec.WitnessClassReason, NeverServe: rec.NeverServe}
		data, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			return err
		}
		entries = append(entries, recordstore.Entry{Name: fileName(rec), Data: data})
	}
	return store.Install(1, entries...)
}

// GC removes the corpus's records whose identity no binding of the
// current records names, and every record Load would refuse; the count
// of removed and kept records is returned. A missing store removes
// nothing.
func GC(dir string, live func(selection, symbol string) bool) (removed, kept int, err error) {
	store, err := open(dir)
	if err != nil {
		return 0, 0, err
	}
	return store.Sweep(func(name string, data []byte) bool {
		// What Load never serves — malformed, field-blind, of a prior
		// version, misnamed — is cost with no servable evidence behind
		// it, whatever its identity.
		rec, ok := decodeRecord(name, data)
		return ok && live(rec.Selection, rec.Symbol)
	})
}
