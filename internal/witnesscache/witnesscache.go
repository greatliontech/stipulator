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
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gofresh/runtimeinput"

	"github.com/greatliontech/stipulator/internal/verify"
)

// Path is the cache file, tree-relative — under .stipulator/cache, which is
// gitignored: fingerprints pin the toolchain and platform, so a committed
// cache would ping-pong across machines.
const Path = ".stipulator/cache/witnesses.json"

const version = 2

// Fingerprint is the serialized gofresh fingerprint — the caller owns the
// wire form (gofresh REQ-fresh-fingerprint-data).
type Fingerprint struct {
	MaximalClosure  string       `json:"maximalClosure"`
	Toolchain       string       `json:"toolchain"`
	BuildConfig     string       `json:"buildConfig"`
	Machine         string       `json:"machine,omitempty"`
	RuntimeConfig   string       `json:"runtimeConfig,omitempty"`
	PurityAssertion string       `json:"purityAssertion,omitempty"`
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
	return gofresh.Fingerprint{
		MaximalClosure: f.MaximalClosure,
		Guards: guard.Guards{
			Toolchain:     f.Toolchain,
			BuildConfig:   f.BuildConfig,
			Machine:       f.Machine,
			RuntimeConfig: f.RuntimeConfig,
		},
		PurityAssertion: f.PurityAssertion,
		RuntimeInputs:   f.RuntimeInputs,
		RuntimeDigest:   f.RuntimeDigest,
		ResultKind:      f.ResultKind,
	}
}

// FromGofresh converts from the engine's form.
func FromGofresh(fp gofresh.Fingerprint) Fingerprint {
	return Fingerprint{
		MaximalClosure:  fp.MaximalClosure,
		Toolchain:       fp.Guards.Toolchain,
		BuildConfig:     fp.Guards.BuildConfig,
		Machine:         fp.Guards.Machine,
		RuntimeConfig:   fp.Guards.RuntimeConfig,
		PurityAssertion: fp.PurityAssertion,
		RuntimeInputs:   fp.RuntimeInputs,
		RuntimeDigest:   fp.RuntimeDigest,
		ResultKind:      fp.ResultKind,
	}
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

type document struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

// Load reads the cache at path under dir; a missing or unreadable-as-cache
// file is an empty cache — absence of proof runs tests, so a corrupt cache
// costs work, never correctness.
func Load(dir string) []Record {
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(Path)))
	if err != nil {
		return nil
	}
	var doc document
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&doc) != nil || dec.Decode(&struct{}{}) != io.EOF || doc.Version != version || doc.Records == nil {
		return nil
	}
	manifests := map[string]bool{}
	seen := map[string]bool{}
	for _, rec := range doc.Records {
		if rec.Package == "" || rec.Test == "" || seen[rec.Key()] || !validOutcomes(rec) || !rec.Fingerprint.valid(dir, manifests) {
			return nil
		}
		seen[rec.Key()] = true
	}
	return doc.Records
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
		validPurity(f.PurityAssertion) && validManifest && validDigest(f.RuntimeDigest) &&
		f.ResultKind == gofresh.CodeResult
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

// Save writes the cache under dir.
func Save(dir string, records []Record) error {
	if records == nil {
		records = []Record{}
	}
	doc := document{Version: version, Records: records}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	full := filepath.Join(dir, filepath.FromSlash(Path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	// Write-then-rename: a concurrent writer (two MCP verifies) must never
	// leave a torn file — a torn cache costs work, but only through Load's
	// unreadable-is-empty leg, and rename makes even that window vanish.
	tmp, err := os.CreateTemp(filepath.Dir(full), ".witnesses-*.json")
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
	return os.Rename(tmp.Name(), full)
}

// EnsureIgnored makes sure the cache directory is never committed: it
// writes a .gitignore beside the cache covering the cache dir when one is
// not already in place.
func EnsureIgnored(dir string) error {
	gi := filepath.Join(dir, ".stipulator", "cache", ".gitignore")
	if _, err := os.Stat(gi); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(gi), 0o755); err != nil {
		return err
	}
	return os.WriteFile(gi, []byte("*\n"), 0o644)
}

// Key is the record's identity.
func (r Record) Key() string { return r.Package + "." + r.Test }
