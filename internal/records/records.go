// Package records loads and rewrites the committed machine-owned records
// adjacent to the corpus: bindings, gaps, and the tombstone registry.
// Records are inputs to verification, never results of it.
package records

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/encoding/prototext"
)

// Store locations, fixed relative to the repository root.
const (
	BindingsDir    = ".stipulator/bindings"
	GapsDir        = ".stipulator/gaps"
	TombstonesPath = ".stipulator/tombstones.textproto"
)

// BindingFile is one committed binding file.
type BindingFile struct {
	Path string
	Raw  []byte
	Set  *stipulatorv1.BindingSet
}

// GapFile is one committed gap record.
type GapFile struct {
	Path string
	Raw  []byte
	Gap  *stipulatorv1.Gap
}

// Store is the loaded record state of a repository.
type Store struct {
	Bindings   []BindingFile
	Gaps       []GapFile
	Tombstones []string
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
	tombs, err := LoadTombstones(fsys)
	if err != nil {
		return nil, err
	}
	s.Tombstones = tombs
	return s, nil
}

// LoadTombstones reads the tombstone registry; an absent registry means
// nothing has been retired.
func LoadTombstones(fsys fs.FS) ([]string, error) {
	b, err := fs.ReadFile(fsys, TombstonesPath)
	if err != nil {
		return nil, nil
	}
	t := &stipulatorv1.Tombstones{}
	if err := prototext.Unmarshal(b, t); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", TombstonesPath, err)
	}
	return t.GetRetired(), nil
}

// eachTextproto visits the .textproto files of a directory in lexical
// order; an absent directory is empty.
func eachTextproto(fsys fs.FS, dir string, fn func(string, []byte) error) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".textproto") {
			continue
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
