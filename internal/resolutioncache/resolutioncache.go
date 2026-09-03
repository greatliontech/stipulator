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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/greatliontech/gofresh"
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
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(root, "stipulator", "resolutions", hex.EncodeToString(sum[:8])), nil
}

func digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func fingerprintDigest(f witnesscache.Fingerprint) string {
	data, err := json.Marshal(f)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func fileName(r Record) string {
	return digest(r.Selection, r.Symbol) + "-" + fingerprintDigest(r.Fingerprint) + ".json"
}

// Load reads every record of the corpus rooted at dir, in file-name
// order; a missing store is empty, and a malformed, field-blind,
// prior-version, or observation-carrying record is skipped.
func Load(dir string) []Record {
	store, err := StoreDir(dir)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []Record
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(store, name))
		if err != nil {
			continue
		}
		var e entry
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if dec.Decode(&e) != nil || e.Version != version || e.Selection == "" || e.Symbol == "" || !validResolution(e.Resolution) || !validFingerprint(e.Fingerprint) {
			continue
		}
		out = append(out, Record{
			Selection: e.Selection, Symbol: e.Symbol, Fingerprint: e.Fingerprint,
			Resolution: e.Resolution, Shape: e.Shape, Package: e.Package,
			WitnessClass: e.WitnessClass, WitnessClassReason: e.WitnessClassReason, NeverServe: e.NeverServe,
		})
	}
	return out
}

func validResolution(r string) bool { return r == "resolved" || r == "generated_file" }

// validFingerprint admits source-closure tiers only — the maximal
// closure, the test-variant compartment, the toolchain, the build
// configuration, all present: a resolution observes nothing, so any
// observation, purity, or runtime tier marks a record this store did
// not write.
func validFingerprint(f witnesscache.Fingerprint) bool {
	return validDigest(f.MaximalClosure) && validDigest(f.TestVariantClosure) && f.Toolchain != "" && validDigest(f.BuildConfig) &&
		f.ResultKind == gofresh.CodeResult &&
		f.Machine == "" && f.RuntimeConfig == "" && f.ObservationAssertion == "" && f.ObservationProof == nil &&
		f.PurityAssertion == "" && f.DynamicStateVouches == "" && f.RuntimeInputs == "" && f.RuntimeDigest == ""
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16 && strings.ToLower(value) == value
}

// Install atomically writes one record; a record for the same selection
// and symbol under another fingerprint is replaced, so the store holds
// one record per identity.
func Install(dir string, rec Record) error { return InstallAll(dir, []Record{rec}) }

// InstallAll installs a batch of records with one scan of the store for
// the superseded identities: the cold publish over a corpus writes
// hundreds, and a per-record scan would be quadratic in the store.
func InstallAll(dir string, recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	store, err := StoreDir(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(store, 0o755); err != nil {
		return err
	}
	written := map[string]string{}
	for _, rec := range recs {
		name, err := write(store, rec)
		if err != nil {
			return err
		}
		written[digest(rec.Selection, rec.Symbol)] = name
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		identity, _, ok := strings.Cut(e.Name(), "-")
		if !ok {
			continue
		}
		// One record per identity: prior fingerprints of the same
		// selection and symbol are superseded.
		if keep, superseded := written[identity]; superseded && e.Name() != keep {
			os.Remove(filepath.Join(store, e.Name()))
		}
	}
	return nil
}

// write atomically writes one record's file and returns its name.
func write(store string, rec Record) (string, error) {
	if !validResolution(rec.Resolution) || !validFingerprint(rec.Fingerprint) || rec.Selection == "" || rec.Symbol == "" {
		return "", errors.New("resolutioncache: record carries a field this store does not serve")
	}
	e := entry{Version: version, Selection: rec.Selection, Symbol: rec.Symbol, Fingerprint: rec.Fingerprint, Resolution: rec.Resolution, Shape: rec.Shape, Package: rec.Package, WitnessClass: rec.WitnessClass, WitnessClassReason: rec.WitnessClassReason, NeverServe: rec.NeverServe}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return "", err
	}
	full := filepath.Join(store, fileName(rec))
	if err := witnesscache.WriteAtomic(store, ".record-*.json", full, data); err != nil {
		return "", err
	}
	return filepath.Base(full), nil
}

// GC removes the corpus's records whose identity no binding of the
// current records names; the count of removed and kept records is
// returned. A missing store removes nothing.
func GC(dir string, live func(selection, symbol string) bool) (removed, kept int, err error) {
	store, err := StoreDir(dir)
	if err != nil {
		return 0, 0, err
	}
	entries, err := os.ReadDir(store)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(store, e.Name()))
		var rec entry
		if readErr != nil || json.Unmarshal(data, &rec) != nil || rec.Version != version || !live(rec.Selection, rec.Symbol) {
			if rmErr := os.Remove(filepath.Join(store, e.Name())); rmErr == nil {
				removed++
			}
			continue
		}
		kept++
	}
	return removed, kept, nil
}
