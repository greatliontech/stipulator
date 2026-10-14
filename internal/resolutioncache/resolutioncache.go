// Package resolutioncache is the machine-local store of binding
// resolutions served by proven equivalence: one record per (build
// selection, symbol) carrying the resolution's every served field beside
// the Gofresh fingerprint of the symbol's source closure and a complete
// selected-input certificate, served exactly when both match the current tree
// (REQ-evidence-resolution-freshness). The layout mirrors the witness
// cache's (REQ-evidence-resolution-cache-format): the store is keyed by
// the corpus root, records are versioned, and a field-blind or
// prior-version record is ignored rather than read.
package resolutioncache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// version is the record format: bumped when a persisted field is added
// that the serving decision consumes, or when the classifier's contract
// changes what a record proves — either way a prior record fails closed
// to a typed resolution.
// Complete cross-view certificates are required; older records lack them.
const version = 5

// Derivation identifies the rules behind the recorded source fact, apart
// from the envelope version. A classifier or lookup-rule change moves it.
const Derivation = "stipulator/source-resolution@1"

// Certificate binds a source derivation to its complete selected inputs.
// It is not evidence of executing the subject it describes.
type Certificate struct {
	Derivation string `json:"derivation"`
	Inputs     string `json:"inputs"`
}

func (c Certificate) valid() bool {
	if c.Derivation != Derivation || len(c.Inputs) != 64 {
		return false
	}
	for _, r := range c.Inputs {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (c *Certificate) UnmarshalJSON(data []byte) error {
	fields, ok := objectFields(data, "derivation", "inputs")
	var value Certificate
	if !ok || len(fields) != 2 || json.Unmarshal(fields["derivation"], &value.Derivation) != nil || json.Unmarshal(fields["inputs"], &value.Inputs) != nil || !value.valid() {
		return errors.New("resolutioncache: incomplete or incompatible source certificate")
	}
	*c = value
	return nil
}

// Record is one served resolution: the symbol under its resolving build
// selection, the fingerprint and certificate proving its inputs, and every field a
// verification reads from a resolution.
type Record struct {
	// Selection is the resolving build selection's key — the effective
	// tag set, toolchain, module mode and root/workspace context — so a record
	// serves only the view that produced it (REQ-go-build-selections).
	Selection string
	Symbol    string
	// Fingerprint is the Gofresh fingerprint of the symbol as a subject:
	// source-closure tiers only, never an observation.
	Fingerprint witnesscache.Fingerprint
	Certificate Certificate
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
	Certificate        Certificate              `json:"certificate"`
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

// FileName is the record's file: the identity digest over the selection
// key and symbol, joined with its fingerprint and certificate digest
// (REQ-evidence-resolution-cache-format).
func (r Record) FileName() (string, error) {
	return recordstore.Name([]string{r.Selection, r.Symbol}, struct {
		Fingerprint witnesscache.Fingerprint `json:"fingerprint"`
		Certificate Certificate              `json:"certificate"`
	}{r.Fingerprint, r.Certificate})
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
	if _, ok := objectFields(data, "version", "selection", "symbol", "fingerprint", "certificate", "resolution", "shape", "package", "witnessClass", "witnessClassReason", "neverServe"); !ok {
		return Record{}, false
	}
	var e entry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&e) != nil || dec.Decode(&struct{}{}) != io.EOF || e.Version != version || e.Selection == "" || e.Symbol == "" || !validResolution(e.Resolution) || !Admits(e.Fingerprint) || !e.Certificate.valid() {
		return Record{}, false
	}
	rec := Record{
		Selection: e.Selection, Symbol: e.Symbol, Fingerprint: e.Fingerprint,
		Certificate: e.Certificate,
		Resolution:  e.Resolution, Shape: e.Shape, Package: e.Package,
		WitnessClass: e.WitnessClass, WitnessClassReason: e.WitnessClassReason, NeverServe: e.NeverServe,
	}
	if !validAnswer(rec) {
		return Record{}, false
	}
	// A decoded fingerprint passed Gofresh's decoder, whose ladder is the
	// encoder's, so it always names a file; a file without the member
	// decodes to the zero fingerprint, whose empty name matches no file.
	want, _ := rec.FileName()
	if name != want {
		return Record{}, false
	}
	return rec, true
}

func validResolution(r string) bool { return r == "resolved" || r == "generated_file" }

func validAnswer(r Record) bool {
	rest, ok := strings.CutPrefix(r.Symbol, r.Package+".")
	return r.Package != "" && ok && rest != "" &&
		(r.Resolution != "resolved" || r.Shape != "") &&
		(r.WitnessClass == "proof" || r.WitnessClass == "property" || r.WitnessClass == "example")
}

// SourceTiers projects a capture onto the parts this store serves — the
// maximal closure, the test-variant compartment, the closure strategy
// that derived them, native toolchain content and admission policy, the
// toolchain, the build configuration, the result
// kind — by copying exactly those into a zero fingerprint, so a field
// gofresh adds later cannot pass through unseen: a resolution is a
// source-derived fact, and a subject's purity
// assertion, observation proof, vouches, discharges, runtime inputs, or
// machine guard say nothing about where its declaration lives. The
// publisher projects every capture through it; a record is then
// admitted or refused on those parts (Admits) beside the complete certificate.
func SourceTiers(f witnesscache.Fingerprint) witnesscache.Fingerprint {
	return witnesscache.Fingerprint{
		MaximalClosure:     f.MaximalClosure,
		TestVariantClosure: f.TestVariantClosure,
		ClosureStrategy:    f.ClosureStrategy,
		ToolchainContent:   f.ToolchainContent,
		AdmissionPolicy:    f.AdmissionPolicy,
		Guards:             guard.Guards{Toolchain: f.Guards.Toolchain, BuildConfig: f.Guards.BuildConfig},
		ResultKind:         f.ResultKind,
	}
}

// Admits reports whether a fingerprint is one this store serves —
// source-closure tiers only: the maximal closure, the test-variant
// compartment, the toolchain, the build configuration, all present, and
// no observation, purity, vouch, discharge, runtime, or machine tier
// set, which would mark a record this store did not write. The shape
// SourceTiers yields from a complete capture.
func Admits(f witnesscache.Fingerprint) bool {
	return witnesscache.ValidDigest(f.MaximalClosure) && witnesscache.ValidDigest(f.TestVariantClosure) && f.Guards.Toolchain != "" && witnesscache.ValidDigest(f.Guards.BuildConfig) && f.ClosureStrategy != "" && f.ToolchainContent != "" && f.AdmissionPolicy != "" &&
		f.ResultKind == gofresh.CodeResult &&
		f.Guards.Machine == "" && f.Guards.RuntimeConfig == "" && f.ObservationAssertion == "" && f.ObservationProof == (gofresh.ObservationProof{}) &&
		f.PurityAssertion == "" && f.DynamicStateVouches == "" && f.RuntimeInputs == "" && f.RuntimeDigest == "" &&
		f.SingleSubjectDischarges == "" && f.PackageProcessDischarges == "" && f.DynamicStateStrategy == ""
}

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
		if !validResolution(rec.Resolution) || !Admits(rec.Fingerprint) || !rec.Certificate.valid() || !validAnswer(rec) || rec.Selection == "" || rec.Symbol == "" {
			return errors.New("resolutioncache: record carries a field this store does not serve")
		}
		// Admits admitted a code result with no measurement
		// guard — the encoder's own ladder — so the name always exists.
		name, _ := rec.FileName()
		e := entry{Version: version, Selection: rec.Selection, Symbol: rec.Symbol, Fingerprint: rec.Fingerprint, Certificate: rec.Certificate, Resolution: rec.Resolution, Shape: rec.Shape, Package: rec.Package, WitnessClass: rec.WitnessClass, WitnessClassReason: rec.WitnessClassReason, NeverServe: rec.NeverServe}
		data, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			return err
		}
		entries = append(entries, recordstore.Entry{Name: name, Data: data})
	}
	return store.Install(1, entries...)
}

// GC removes the corpus's records whose identity no binding of the
// current records names, and every record Load would refuse; the count
// of removed and kept records is returned. A missing store removes
// nothing.
func GC(ctx context.Context, dir string, live func(selection, symbol string) bool) (removed, kept int, err error) {
	store, err := open(dir)
	if err != nil {
		return 0, 0, err
	}
	return store.Sweep(ctx, func(name string, data []byte) bool {
		// What Load never serves — malformed, field-blind, of a prior
		// version, misnamed — is cost with no servable evidence behind
		// it, whatever its identity.
		rec, ok := decodeRecord(name, data)
		return ok && live(rec.Selection, rec.Symbol)
	})
}
