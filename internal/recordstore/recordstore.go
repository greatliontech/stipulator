// Package recordstore is the machine-local record store's mechanics,
// shared by every record kind stipulator persists under the user cache
// directory: the directory layout keyed by the corpus root, the file
// naming that joins a record identity's digest with its fingerprint's,
// the recency-ordered scan, the atomic install bounded per identity,
// and the garbage-collection walk (REQ-evidence-record-store-layout).
// A kind owns its record's encoding, admission, and retention bound;
// the store owns nothing a record means. Two kinds exist today — the
// witness cache and the resolution cache — and both are compositions
// of these primitives, so a moved cache root, a changed digest width,
// or a changed eviction rule is one edit.
package recordstore

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
)

// Root is the cache directory every stipulator kind lives under:
// <user cache>/stipulator/<kind>.
func Root(kind string) (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "stipulator", kind), nil
}

// Store is one kind's directory for one corpus.
type Store struct {
	kind string
	path string
}

// Open is the store of kind for the corpus rooted at dir: under Root,
// keyed by the digest of the root's resolved absolute path — never
// inside the repository, so a committed cache cannot ping-pong across
// machines and a fresh worktree's cache does not die with it.
func Open(kind, dir string) (Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Store{}, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	root, err := Root(kind)
	if err != nil {
		return Store{}, err
	}
	return Store{kind: kind, path: filepath.Join(root, Digest(abs))}, nil
}

// Path is the store's directory.
func (s Store) Path() string { return s.path }

// Digest is the store's one digest: the first sixteen hexadecimal
// characters of SHA-256 over the NUL-joined parts — a filename-length
// economy over the one hash the model defines, never a second hash
// function (REQ-model-hash-func).
func Digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

// Name is a record file's name: the identity's digest joined with the
// digest of the fingerprint's canonical JSON encoding. A kind reads a
// file only when its name agrees with the record inside, so the
// truncated digests' collision risk is absorbed per file.
func Name(identity []string, fingerprint any) string {
	return Digest(identity...) + "-" + fingerprintDigest(fingerprint) + ".json"
}

func fingerprintDigest(fingerprint any) string {
	data, err := json.Marshal(fingerprint)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// identityOf is the identity segment a file name carries; a name
// without one belongs to no identity.
func identityOf(name string) string {
	identity, _, ok := strings.Cut(name, "-")
	if !ok {
		return ""
	}
	return identity
}

// pattern is the install temporary's name: dot-prefixed, so the scan
// never reads a record mid-rename and a sweep never claims it — the
// store's own, never a kind's to get wrong.
func (s Store) pattern() string { return "." + s.kind + "-*.json" }

// Names lists every record file of the store, most recently installed
// first with names breaking ties: a serving loop's first round then
// tries the variant the last state change produced, and a reader
// meeting an identity more than once takes the first. Directories,
// dot-prefixed install temporaries, and files without the .json suffix
// are never records. A missing or unreadable store is the error.
func (s Store) Names() ([]string, error) {
	files, err := s.scan()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.name)
	}
	return names, nil
}

// Read is one record file's bytes; a kind reads records one at a time,
// so a store of any size costs one record's memory to load.
func (s Store) Read(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.path, name))
}

type aged struct {
	name string
	mod  int64
}

// scan lists the record files newest first, names breaking ties.
func (s Store) scan() ([]aged, error) {
	entries, err := os.ReadDir(s.path)
	if err != nil {
		return nil, err
	}
	var files []aged
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		var mod int64
		if info, err := e.Info(); err == nil {
			mod = info.ModTime().UnixNano()
		}
		files = append(files, aged{e.Name(), mod})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].mod != files[j].mod {
			return files[i].mod > files[j].mod
		}
		return files[i].name < files[j].name
	})
	return files, nil
}

// Entry is one record file to install.
type Entry struct {
	Name string
	Data []byte
}

// Install lands every entry atomically — the store's temporary and a
// rename, so a concurrent reader never sees a torn file and a
// failed write leaves nothing behind — then bounds each touched
// identity's variants: ranked by recency, the batch's entries latest
// first ahead of the store's prior files newest first, an identity's
// first bound variants stay and the rest are evicted. A later entry of
// the batch thus supersedes an earlier one of its identity, and one
// scan serves the whole batch, so a cold publish of hundreds of records
// is linear in the store. Eviction costs only execution: on mtime ties
// a concurrent installer's fresh variant can go, and its next run
// re-records it — never wrong serving.
func (s Store) Install(bound int, entries ...Entry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(s.path, 0o755); err != nil {
		return err
	}
	var rank []string
	written := map[string]bool{}
	touched := map[string]bool{}
	for _, e := range entries {
		if err := WriteAtomic(s.path, s.pattern(), filepath.Join(s.path, e.Name), e.Data); err != nil {
			return err
		}
		touched[identityOf(e.Name)] = true
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if name := entries[i].Name; !written[name] {
			written[name] = true
			rank = append(rank, name)
		}
	}
	prior, err := s.scan()
	if err != nil {
		return err
	}
	for _, f := range prior {
		if !written[f.name] {
			rank = append(rank, f.name)
		}
	}
	kept := map[string]int{}
	for _, name := range rank {
		identity := identityOf(name)
		if !touched[identity] {
			continue
		}
		if kept[identity] < bound {
			kept[identity]++
			continue
		}
		os.Remove(filepath.Join(s.path, name))
	}
	return nil
}

// WriteAtomic lands data at full through a temporary in dir matching
// pattern and a rename, so a concurrent reader never sees a torn file
// and a failed write leaves nothing behind.
func WriteAtomic(dir, pattern, full string, data []byte) error {
	tmp, err := os.CreateTemp(dir, pattern)
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
	return nil
}

// Sweep is the garbage-collection walk: every record file keep refuses
// is removed, the counts of removed and kept files are returned, and
// the first removal failure is the error while the walk and its counts
// continue. A file that cannot be read reaches keep with no data, so
// the kind refuses it and it goes (cost with no servable evidence
// behind it). Install temporaries are never swept — a concurrent
// installer's rename is about to claim them. A missing store removes
// nothing; a store that cannot be read is the error.
func (s Store) Sweep(keep func(name string, data []byte) bool) (removed, kept int, err error) {
	names, err := s.Names()
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	for _, name := range names {
		data, readErr := s.Read(name)
		if readErr != nil {
			data = nil
		}
		if keep(name, data) {
			kept++
			continue
		}
		if rmErr := os.Remove(filepath.Join(s.path, name)); rmErr == nil {
			removed++
		} else if err == nil {
			err = rmErr
		}
	}
	return removed, kept, err
}
