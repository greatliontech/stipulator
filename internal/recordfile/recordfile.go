// Package recordfile is the one read every record file goes through —
// the record stores' textprotos, the tombstone registry, the corpus
// manifest — so the one posture toward an empty file is held once: no
// record kind is ever written empty (an emptied set is deleted), so an
// empty file at a record name is the residue of a write that died
// between its exclusive reservation and its rename, or another
// process's write still in flight, never a record with no content — a
// gap read as naming no requirement or a registry read as retiring
// nothing would vanish from every surface silently.
package recordfile

import (
	"fmt"
	"io/fs"
)

// Read returns the file's bytes, refusing an empty file by name; every
// error names the path, so callers wrap nothing.
func Read(fsys fs.FS, path string) ([]byte, error) {
	raw, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%s is empty: a record write's reservation, left by a fault between its exclusive create and its rename or by another process's write still in flight (no record is ever written empty); re-run the verb, and remove the file if it stays empty", path)
	}
	return raw, nil
}
