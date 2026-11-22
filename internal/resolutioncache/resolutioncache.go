// Package resolutioncache is the machine-local store of binding
// resolutions served by proven equivalence: one record per (build
// selection, symbol) carrying the resolution's every served field beside
// the ordered multi-view Gofresh SourceResult entries, served exactly when
// every view matches the current tree (REQ-evidence-resolution-freshness).
// The layout mirrors the witness cache's (REQ-evidence-resolution-cache-format):
// the store is keyed by the corpus root, records are versioned, and a
// field-blind or prior-version record is ignored rather than read.
package resolutioncache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// version is the record format: bumped when a persisted field is added
// that the serving decision consumes, or when the classifier's contract
// changes what a record proves — either way a prior record fails closed
// to a typed resolution.
// Version 7 stores ordered multi-view SourceResult entries (`views`) for
// callable and non-callable declarations alike; prior versions fail closed.
const version = 7

// ViewEntry records one selected view's contribution to a resolution record
// in policy precedence order (REQ-evidence-resolution-freshness,
// REQ-evidence-resolution-cache-format): either the view declared the symbol
// (Present == true, carrying its SourceResult Fingerprint) or the symbol was
// absent in that view (Present == false, zero Fingerprint).
type ViewEntry struct {
	Selection   string                   `json:"selection"`
	Present     bool                     `json:"present"`
	Fingerprint witnesscache.Fingerprint `json:"fingerprint,omitzero"`
}

// AdmitsView reports whether v is a complete, valid multi-view entry.
func AdmitsView(v ViewEntry) bool {
	if v.Selection == "" {
		return false
	}
	if !v.Present {
		return v.Fingerprint == (witnesscache.Fingerprint{})
	}
	return Admits(v.Fingerprint)
}

func (v *ViewEntry) UnmarshalJSON(data []byte) error {
	fields, ok := objectFields(data, "selection", "present", "fingerprint")
	if !ok || fields["selection"] == nil || fields["present"] == nil {
		return errors.New("resolutioncache: incomplete or incompatible view entry")
	}
	var value ViewEntry
	if json.Unmarshal(fields["selection"], &value.Selection) != nil || json.Unmarshal(fields["present"], &value.Present) != nil {
		return errors.New("resolutioncache: invalid view entry selection or presence")
	}
	if value.Present {
		if fields["fingerprint"] == nil || json.Unmarshal(fields["fingerprint"], &value.Fingerprint) != nil {
			return errors.New("resolutioncache: present view entry requires valid SourceResult fingerprint")
		}
	} else if fields["fingerprint"] != nil {
		return errors.New("resolutioncache: absent view entry must omit fingerprint")
	}
	if !AdmitsView(value) {
		return errors.New("resolutioncache: invalid view entry")
	}
	*v = value
	return nil
}

// Record is one served resolution: the symbol under its resolving build
// selection, the ordered multi-view SourceResult entries proving its inputs,
// and every field a verification reads from a resolution.
type Record struct {
	// Selection is the resolving build selection's key — the effective
	// tag set, toolchain, module mode and root/workspace context — so a record
	// serves only the view precedence that produced it (REQ-go-build-selections).
	Selection string
	Symbol    string
	// Views records every policy build selection in precedence order:
	// views before Selection have Present == false, Selection itself has
	// Present == true with its SourceResult Fingerprint, and views after
	// Selection record whether they declare the symbol and, when present,
	// their SourceResult Fingerprint.
	Views []ViewEntry
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

// Equal reports whether r and other carry identical fields and view entries.
func (r Record) Equal(other Record) bool {
	return r.Selection == other.Selection &&
		r.Symbol == other.Symbol &&
		slices.Equal(r.Views, other.Views) &&
		r.Resolution == other.Resolution &&
		r.Shape == other.Shape &&
		r.Package == other.Package &&
		r.WitnessClass == other.WitnessClass &&
		r.WitnessClassReason == other.WitnessClassReason &&
		r.NeverServe == other.NeverServe
}

// Clone returns an independent copy of r with its Views slice cloned.
func (r Record) Clone() Record {
	r.Views = slices.Clone(r.Views)
	return r
}

// Key is the record's identity within the store.
func (r Record) Key() string { return r.Selection + "\x00" + r.Symbol }

type entry struct {
	Version            int         `json:"version"`
	Selection          string      `json:"selection"`
	Symbol             string      `json:"symbol"`
	Views              []ViewEntry `json:"views"`
	Resolution         string      `json:"resolution"`
	Shape              string      `json:"shape,omitempty"`
	Package            string      `json:"package,omitempty"`
	WitnessClass       string      `json:"witnessClass,omitempty"`
	WitnessClassReason string      `json:"witnessClassReason,omitempty"`
	NeverServe         string      `json:"neverServe,omitempty"`
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
// key and symbol, joined with the digest of its ordered Views array
// (REQ-evidence-resolution-cache-format).
func (r Record) FileName() (string, error) {
	return recordstore.Name([]string{r.Selection, r.Symbol}, struct {
		Views []ViewEntry `json:"views"`
	}{r.Views})
}

// Load reads every record of the corpus rooted at dir, most recently
// installed first; a missing store is empty, and a malformed,
// field-blind, prior-version, misnamed, or non-source-tier record
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
	fields, ok := objectFields(data, "version", "selection", "symbol", "views", "resolution", "shape", "package", "witnessClass", "witnessClassReason", "neverServe")
	if !ok || fields["version"] == nil || fields["selection"] == nil || fields["symbol"] == nil || fields["views"] == nil || fields["resolution"] == nil || fields["package"] == nil || fields["witnessClass"] == nil {
		return Record{}, false
	}
	var e entry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&e) != nil || dec.Decode(&struct{}{}) != io.EOF || e.Version != version {
		return Record{}, false
	}
	rec := Record{
		Selection: e.Selection, Symbol: e.Symbol, Views: slices.Clone(e.Views),
		Resolution: e.Resolution, Shape: e.Shape, Package: e.Package,
		WitnessClass: e.WitnessClass, WitnessClassReason: e.WitnessClassReason, NeverServe: e.NeverServe,
	}
	if !AdmitsRecord(rec) {
		return Record{}, false
	}
	want, err := rec.FileName()
	if err != nil || name != want {
		return Record{}, false
	}
	return rec, true
}

// AdmitsRecord reports whether a resolution record is complete and valid
// to install or serve: a valid resolved answer whose ordered Views entries
// are unique, valid, absent before r.Selection, and present at r.Selection.
func AdmitsRecord(r Record) bool {
	if r.Selection == "" || r.Symbol == "" || !validResolution(r.Resolution) || !validAnswer(r) || len(r.Views) == 0 {
		return false
	}
	seen := make(map[string]bool, len(r.Views))
	foundResolving := false
	for _, v := range r.Views {
		if !AdmitsView(v) || seen[v.Selection] {
			return false
		}
		seen[v.Selection] = true
		if !foundResolving {
			if v.Selection == r.Selection {
				if !v.Present {
					return false
				}
				foundResolving = true
			} else if v.Present {
				return false
			}
		}
	}
	return foundResolving
}

func validResolution(r string) bool { return r == "resolved" || r == "generated_file" }

func validAnswer(r Record) bool {
	rest, ok := strings.CutPrefix(r.Symbol, r.Package+".")
	return r.Package != "" && ok && rest != "" &&
		(r.Resolution != "resolved" || r.Shape != "") &&
		(r.WitnessClass == "proof" || r.WitnessClass == "property" || r.WitnessClass == "example")
}

// Admits reports whether a fingerprint is one this store serves —
// Gofresh SourceResult tiers only: maximal closure, test-variant
// compartment, closure strategy, native toolchain content and admission
// policy, toolchain guard, build-configuration guard, and SourceResult kind,
// with no observation, purity, vouch, discharge, applicability-extension,
// runtime, or machine tier set.
func Admits(f witnesscache.Fingerprint) bool {
	return f.Validate() == nil &&
		f.ResultKind == gofresh.SourceResult &&
		witnesscache.ValidDigest(f.MaximalClosure) &&
		witnesscache.ValidDigest(f.TestVariantClosure) &&
		f.Guards.Toolchain != "" &&
		witnesscache.ValidDigest(f.Guards.BuildConfig) &&
		f.ClosureStrategy != "" &&
		f.ToolchainContent != "" &&
		f.AdmissionPolicy != "" &&
		f.InertTestVariantApplicability == (gofresh.InertTestVariantApplicability{}) &&
		f.Guards.Machine == "" &&
		f.Guards.RuntimeConfig == "" &&
		f.ObservationAssertion == "" &&
		f.ObservationProof == (gofresh.ObservationProof{}) &&
		f.PurityAssertion == "" &&
		f.DynamicStateVouches == "" &&
		f.RuntimeInputs == "" &&
		f.RuntimeDigest == "" &&
		f.SingleSubjectDischarges == "" &&
		f.PackageProcessDischarges == "" &&
		f.DynamicStateStrategy == ""
}

// InstallAll installs a batch of records under the store's one-record
// retention: the cold publish over a corpus writes hundreds, and the
// store's one scan supersedes every prior view sequence of a written
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
		if !AdmitsRecord(rec) {
			return errors.New("resolutioncache: record carries a field this store does not serve")
		}
		name, err := rec.FileName()
		if err != nil {
			return err
		}
		e := entry{
			Version: version, Selection: rec.Selection, Symbol: rec.Symbol,
			Views: slices.Clone(rec.Views), Resolution: rec.Resolution,
			Shape: rec.Shape, Package: rec.Package, WitnessClass: rec.WitnessClass,
			WitnessClassReason: rec.WitnessClassReason, NeverServe: rec.NeverServe,
		}
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
		rec, ok := decodeRecord(name, data)
		return ok && live(rec.Selection, rec.Symbol)
	})
}

// PruneGlobal performs global usage/preview and bounded reclamation across all
// resolution stores under the user cache directory.
func PruneGlobal(ctx context.Context, opts recordstore.PruneOptions) (recordstore.PruneReport, error) {
	return recordstore.PruneKind(ctx, "resolutions", opts, nil)
}
