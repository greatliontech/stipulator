// Package records loads and rewrites the committed machine-owned records
// adjacent to the corpus: bindings, gaps, and the tombstone registry.
// Records are inputs to verification, never results of it.
package records

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/encoding/prototext"
)

// Store locations, fixed relative to the repository root.
const (
	BindingsDir     = ".stipulator/bindings"
	GapsDir         = ".stipulator/gaps"
	AttestationsDir = ".stipulator/attestations"
	TombstonesPath  = ".stipulator/tombstones.textproto"
)

// BindingFile is one committed binding file.
type BindingFile struct {
	Path string
	Raw  []byte
	Set  *stipulatorv1.BindingSet
}

// HardeningFile is one committed kill-sheet file — exploration records,
// read for cache reuse and staleness reporting, never by coverage.
type HardeningFile struct {
	Path string
	Raw  []byte
	Set  *stipulatorv1.HardeningSet
}

// GapFile is one committed gap record.
type GapFile struct {
	Path string
	Raw  []byte
	Gap  *stipulatorv1.Gap
}

// AttestationFile is one committed attestation record file.
type AttestationFile struct {
	Path string
	Raw  []byte
	Set  *stipulatorv1.AttestationSet
}

// Store is the loaded record state of a repository.
type Store struct {
	Bindings     []BindingFile
	Gaps         []GapFile
	Attestations []AttestationFile
	Hardening    []HardeningFile
	Tombstones   []string
}

// Load reads all records from fsys, which must be rooted at the repository
// root. Absent directories load as empty: a repository with no records is
// simply unbound.
func Load(fsys fs.FS) (*Store, error) {
	s := &Store{}
	if err := eachTextproto(fsys, BindingsDir, func(p string, raw []byte) error {
		set := &stipulatorv1.BindingSet{}
		if err := prototext.Unmarshal(raw, set); err != nil {
			return fmt.Errorf("parsing %s: %w", p, err)
		}
		s.Bindings = append(s.Bindings, BindingFile{Path: p, Raw: raw, Set: set})
		return nil
	}); err != nil {
		return nil, err
	}
	if err := eachTextproto(fsys, GapsDir, func(p string, raw []byte) error {
		gap := &stipulatorv1.Gap{}
		if err := prototext.Unmarshal(raw, gap); err != nil {
			return fmt.Errorf("parsing %s: %w", p, err)
		}
		s.Gaps = append(s.Gaps, GapFile{Path: p, Raw: raw, Gap: gap})
		return nil
	}); err != nil {
		return nil, err
	}
	if err := eachTextproto(fsys, AttestationsDir, func(p string, raw []byte) error {
		set := &stipulatorv1.AttestationSet{}
		if err := prototext.Unmarshal(raw, set); err != nil {
			return fmt.Errorf("parsing %s: %w", p, err)
		}
		s.Attestations = append(s.Attestations, AttestationFile{Path: p, Raw: raw, Set: set})
		return nil
	}); err != nil {
		return nil, err
	}
	if err := eachTextproto(fsys, HardeningDir, func(p string, raw []byte) error {
		set := &stipulatorv1.HardeningSet{}
		// The hardening store is the one non-authoritative record class —
		// exploration findings, never gate input — so it alone tolerates an
		// unknown field, discarding it and re-measuring rather than aborting
		// the load. That is what lets a sheet written before a pin was added
		// (e.g. the pre-content-pin `witnesses` field) load and re-stale
		// instead of bricking the tree. The authoritative stores above stay
		// strict: a typo'd field there must never silently drop a claim.
		if err := (prototext.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, set); err != nil {
			return fmt.Errorf("parsing %s: %w", p, err)
		}
		s.Hardening = append(s.Hardening, HardeningFile{Path: p, Raw: raw, Set: set})
		return nil
	}); err != nil {
		return nil, err
	}
	tombs, err := LoadTombstones(fsys)
	if err != nil {
		return nil, err
	}
	s.Tombstones = tombs
	return s, nil
}

// LoadTombstones reads the tombstone registry; an absent registry means
// nothing has been retired, but any other read failure propagates — an
// unreadable registry must never let a retired identity redeclare.
func LoadTombstones(fsys fs.FS) ([]string, error) {
	b, err := fs.ReadFile(fsys, TombstonesPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", TombstonesPath, err)
	}
	t := &stipulatorv1.Tombstones{}
	if err := prototext.Unmarshal(b, t); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", TombstonesPath, err)
	}
	return t.GetRetired(), nil
}

// eachTextproto visits the .textproto files of a directory in lexical
// order. An absent directory is empty; any other read failure propagates.
// A stray non-.textproto file is an error — a typoed record extension must
// not make the claim silently invisible.
func eachTextproto(fsys fs.FS, dir string, fn func(string, []byte) error) error {
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".textproto") {
			return fmt.Errorf("%s: unexpected file %q in a record directory (records are .textproto)", dir, e.Name())
		}
		p := path.Join(dir, e.Name())
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		if err := fn(p, raw); err != nil {
			return err
		}
	}
	return nil
}
