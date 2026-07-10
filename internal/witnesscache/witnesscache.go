// Package witnesscache is the witness-freshness memoization
// (REQ-evidence-witness-freshness): per top-level test, the gofresh
// fingerprint that produced an outcome set, the outcomes (subtests
// included), and the runtime registrations. The cache is local and
// discardable — never authoritative, never committed: a record serves only
// when its fingerprint checks valid against the current tree, so serving is
// verification by proven equivalence, and absence of proof runs the test.
package witnesscache

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"

	"github.com/greatliontech/stipulator/internal/verify"
)

// Path is the cache file, tree-relative — under .stipulator/cache, which is
// gitignored: fingerprints pin the toolchain and platform, so a committed
// cache would ping-pong across machines.
const Path = ".stipulator/cache/witnesses.json"

const version = 1

// Fingerprint is the serialized gofresh fingerprint — the caller owns the
// wire form (gofresh REQ-fresh-fingerprint-data).
type Fingerprint struct {
	Closure       string `json:"closure"`
	Toolchain     string `json:"toolchain"`
	BuildConfig   string `json:"buildConfig"`
	Machine       string `json:"machine,omitempty"`
	RuntimeConfig string `json:"runtimeConfig,omitempty"`
	RuntimeInputs string `json:"runtimeInputs,omitempty"`
	RuntimeDigest string `json:"runtimeDigest,omitempty"`
}

// ToGofresh converts to the engine's form.
func (f Fingerprint) ToGofresh() gofresh.Fingerprint {
	return gofresh.Fingerprint{
		Closure: f.Closure,
		Guards: guard.Guards{
			Toolchain:     f.Toolchain,
			BuildConfig:   f.BuildConfig,
			Machine:       f.Machine,
			RuntimeConfig: f.RuntimeConfig,
		},
		RuntimeInputs: f.RuntimeInputs,
		RuntimeDigest: f.RuntimeDigest,
	}
}

// FromGofresh converts from the engine's form.
func FromGofresh(fp gofresh.Fingerprint) Fingerprint {
	return Fingerprint{
		Closure:       fp.Closure,
		Toolchain:     fp.Guards.Toolchain,
		BuildConfig:   fp.Guards.BuildConfig,
		Machine:       fp.Guards.Machine,
		RuntimeConfig: fp.Guards.RuntimeConfig,
		RuntimeInputs: fp.RuntimeInputs,
		RuntimeDigest: fp.RuntimeDigest,
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
	if json.Unmarshal(data, &doc) != nil || doc.Version != version {
		return nil
	}
	return doc.Records
}

// Save writes the cache under dir.
func Save(dir string, records []Record) error {
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
