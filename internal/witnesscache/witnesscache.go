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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gofresh/runtimeinput"

	"github.com/greatliontech/stipulator/internal/verify"
)

// The store lives under the user cache directory, keyed by the corpus
// root's absolute resolved path — never inside the repository:
// fingerprints pin the toolchain and platform, so a committed cache would
// ping-pong across machines, and a repo-local one dies with every fresh
// worktree (REQ-evidence-witness-cache-format).
const version = 4

// variantBound caps how many tree-state variants one test identity
// retains; eviction is by install recency and costs only execution.
const variantBound = 4

// StoreDir is the witness store for the corpus rooted at dir.
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
	return filepath.Join(root, "stipulator", "witnesses", hex.EncodeToString(sum[:8])), nil
}

func identityDigest(pkg, test string) string {
	sum := sha256.Sum256([]byte(pkg + "\x00" + test))
	return hex.EncodeToString(sum[:8])
}

func fingerprintDigest(f Fingerprint) string {
	data, err := json.Marshal(f)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// fileName is the record's store name: identity digest joined with
// fingerprint digest, so distinct tree states coexist as variants and a
// name disagreeing with its content is refusable on read.
func fileName(r Record) string {
	return identityDigest(r.Package, r.Test) + "-" + fingerprintDigest(r.Fingerprint) + ".json"
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
	Toolchain            string            `json:"toolchain"`
	BuildConfig          string            `json:"buildConfig"`
	Machine              string            `json:"machine,omitempty"`
	RuntimeConfig        string            `json:"runtimeConfig,omitempty"`
	ObservationAssertion string            `json:"observationAssertion,omitempty"`
	ObservationProof     *observationProof `json:"observationProof,omitempty"`
	PurityAssertion      string            `json:"purityAssertion,omitempty"`
	RuntimeInputs        string            `json:"runtimeInputs,omitempty"`
	RuntimeDigest        string            `json:"runtimeDigest,omitempty"`
	ResultKind           gofresh.Kind      `json:"resultKind"`
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
		MaximalClosure: f.MaximalClosure,
		Guards: guard.Guards{
			Toolchain:     f.Toolchain,
			BuildConfig:   f.BuildConfig,
			Machine:       f.Machine,
			RuntimeConfig: f.RuntimeConfig,
		},
		ObservationAssertion: f.ObservationAssertion,
		PurityAssertion:      f.PurityAssertion,
		RuntimeInputs:        f.RuntimeInputs,
		RuntimeDigest:        f.RuntimeDigest,
		ResultKind:           f.ResultKind,
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
		MaximalClosure:       fp.MaximalClosure,
		Toolchain:            fp.Guards.Toolchain,
		BuildConfig:          fp.Guards.BuildConfig,
		Machine:              fp.Guards.Machine,
		RuntimeConfig:        fp.Guards.RuntimeConfig,
		ObservationAssertion: fp.ObservationAssertion,
		PurityAssertion:      fp.PurityAssertion,
		RuntimeInputs:        fp.RuntimeInputs,
		RuntimeDigest:        fp.RuntimeDigest,
		ResultKind:           fp.ResultKind,
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

// Record is one top-level test's cached witness: the fingerprint that
// produced it, every outcome key it owns ("pkg.Test" and "pkg.Test/sub"),
// and its runtime registrations.
type Record struct {
	Package     string                `json:"package"`
	Test        string                `json:"test"`
	Fingerprint Fingerprint           `json:"fingerprint"`
	Outcomes    map[string]string     `json:"outcomes"`
	Regs        []verify.Registration `json:"registrations,omitempty"`
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
	Package     string                `json:"package"`
	Test        string                `json:"test"`
	Fingerprint Fingerprint           `json:"fingerprint"`
	Outcomes    map[string]string     `json:"outcomes"`
	Regs        []verify.Registration `json:"registrations,omitempty"`
}

// Load reads every variant record of the corpus rooted at dir. A missing
// store is an empty cache, and a malformed, wrong-version, or
// misnamed file is that record alone absent — sibling records stay
// trusted; refusal is per record and costs only that record's execution
// (REQ-evidence-witness-cache-format). One identity may return several
// variants: distinct tree states coexist, and serving picks whichever
// fingerprint proves equivalence.
func Load(dir string) []Record {
	store, err := StoreDir(dir)
	if err != nil {
		return nil
	}
	// The legacy in-repo cache is never read again; remove it best-effort
	// once per load so migrated corpora stop carrying it.
	os.RemoveAll(filepath.Join(dir, ".stipulator", "cache"))
	entries, err := os.ReadDir(store)
	if err != nil {
		return nil
	}
	// ReadDir returns name-sorted entries; dot-prefixed names are install
	// temporaries, never records.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	manifests := map[string]bool{}
	var records []Record
	for _, name := range names {
		rec, ok := loadEntry(store, name, dir, manifests)
		if ok {
			records = append(records, rec)
		}
	}
	return records
}

func loadEntry(store, name, dir string, manifests map[string]bool) (Record, bool) {
	data, err := os.ReadFile(filepath.Join(store, name))
	if err != nil {
		return Record{}, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return Record{}, false
	}
	if value, ok := fields["registrations"]; ok && isJSONNull(value) {
		return Record{}, false
	}
	var e entry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&e) != nil || dec.Decode(&struct{}{}) != io.EOF || e.Version != version {
		return Record{}, false
	}
	rec := Record{Package: e.Package, Test: e.Test, Fingerprint: e.Fingerprint, Outcomes: e.Outcomes, Regs: e.Regs}
	proof := rec.Fingerprint.ObservationProof
	if rec.Package == "" || rec.Test == "" || name != fileName(rec) ||
		(proof != nil && (proof.Package != rec.Package || proof.Symbol != rec.Test)) ||
		!validOutcomes(rec) || !rec.Fingerprint.valid(dir, manifests) {
		return Record{}, false
	}
	return rec, true
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
	return validDigest(f.MaximalClosure) && f.Toolchain != "" && validDigest(f.BuildConfig) &&
		f.Machine == "" && f.RuntimeConfig == "" &&
		validObservation(f) && validPurity(f.PurityAssertion) && validManifest && validDigest(f.RuntimeDigest) &&
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
		validDigest(f.ObservationProof.Evidence)
}

func validDigest(value string) bool {
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
	store, err := StoreDir(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(store, 0o755); err != nil {
		return err
	}
	e := entry{Version: version, Package: rec.Package, Test: rec.Test, Fingerprint: rec.Fingerprint, Outcomes: rec.Outcomes, Regs: rec.Regs}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	full := filepath.Join(store, fileName(rec))
	// Write-then-rename: a concurrent writer must never leave a torn
	// file — a torn variant costs only its own record through the
	// per-file refusal leg, and rename makes even that window vanish.
	tmp, err := os.CreateTemp(store, ".variant-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), full); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	evictBeyondBound(store, identityDigest(rec.Package, rec.Test), filepath.Base(full))
	return nil
}

// evictBeyondBound removes the oldest variants of one identity past
// variantBound, never the just-installed file. On mtime ties a concurrent
// runner's fresh variant can be evicted — execution cost on its next run,
// never wrong serving.
func evictBeyondBound(store, identity, keep string) {
	matches, err := filepath.Glob(filepath.Join(store, identity+"-*.json"))
	if err != nil || len(matches) <= variantBound {
		return
	}
	type aged struct {
		path string
		mod  int64
	}
	var others []aged
	for _, m := range matches {
		if filepath.Base(m) == keep {
			continue
		}
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		others = append(others, aged{m, info.ModTime().UnixNano()})
	}
	sort.Slice(others, func(i, j int) bool { return others[i].mod < others[j].mod })
	for len(others) > variantBound-1 {
		os.Remove(others[0].path)
		others = others[1:]
	}
}

// Key is the record's identity.
func (r Record) Key() string { return r.Package + "." + r.Test }
