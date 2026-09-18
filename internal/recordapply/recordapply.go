// Package recordapply lands a batch of record updates under
// REQ-record-cas, the one applier both faces write through: one apply
// at a time; every path judged admissible before anything else — a
// clean local path under .stipulator/, or a corpus document for an
// update marked as a document rewrite, and on the tree as it stands a
// home resolving under the root, a symlinked directory pointing
// outside refused (REQ-mcp-writes-confined's confinement, held on
// every face); every precondition — the content
// the computing operation read, absence included — checked against
// the live tree before the first write, a moved target refusing the
// whole batch by name; every file staged to a dot-temp before the
// first rename; a file the operation read as absent created
// exclusively — its name reserved with an exclusive create the rename
// then lands over, so one appearing between the precondition and the
// commit fails the reservation instead of being clobbered; a rename
// failing over the reservation removes it, or names it standing, so
// only the process dying between the two leaves an empty file at the
// name — every record load refuses an empty record file as that
// residue, naming it for the operator to remove (no record kind is
// ever written empty: an emptied set is deleted); deletions last —
// a fault between a write and its batch's deletion leaves a duplicate
// the tree shows and prune repairs, where a deletion landing first
// would leave a record lost. A mid-batch fault leaves at most a
// partial state the working tree makes visible — staged temps swept,
// renamed files standing, named in the result beside the error —
// never a silent mix.
package recordapply

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/corpus"
)

// Applier lands batches under one root, reading the tree its owner
// hands it — the one tree that owner reads itself, so the precondition
// and the read it checks never consult two trees; an owner handing
// none reads the root. The staging its writes go through is a seam: a
// face's harness lands writes in an in-memory tree through it; the
// defaults stage a dot-temp beside each target, and only then is the
// path's home judged on the OS at the root (the tree the default
// stager writes), an injected stager owning its placement.
type Applier struct {
	root string
	tree func() fs.FS
	mu   sync.Mutex
	// Stage stages one file's content, returning the commit that lands
	// it and the discard that sweeps an uncommitted stage; nil is the
	// dot-temp default. create marks a file the operation read as
	// absent: the commit fails if the path exists by then. Every stage
	// happens before the first commit.
	Stage func(path string, content []byte, create bool) (commit func() error, discard func(), err error)
	// Remove deletes one path; nil is the root's.
	Remove func(path string) error
}

// New is the applier over the corpus root, reading the tree the caller
// reads — the root itself when tree is nil.
func New(root string, tree func() fs.FS) *Applier {
	if tree == nil {
		tree = func() fs.FS { return os.DirFS(root) }
	}
	return &Applier{root: root, tree: tree}
}

// Appeared is the refusal for a file the operation read as absent that
// exists by the time the batch lands.
func Appeared(path string) error {
	return fmt.Errorf("%s appeared since the operation ran; re-run against the current tree", path)
}

// Result is what an apply landed, in batch order.
type Result struct {
	Wrote   []string
	Deleted []string
}

// Landed spells what a faulted batch landed before its fault, so a
// face's error never reads as "nothing written" over files that moved;
// empty when nothing did.
func (r Result) Landed() string {
	var parts []string
	if len(r.Wrote) > 0 {
		parts = append(parts, "wrote "+strings.Join(r.Wrote, ", "))
	}
	if len(r.Deleted) > 0 {
		parts = append(parts, "deleted "+strings.Join(r.Deleted, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "landed before the fault: " + strings.Join(parts, "; ")
}

func (a *Applier) stage(path string, content []byte, create bool) (func() error, func(), error) {
	if a.Stage != nil {
		return a.Stage(path, content, create)
	}
	full := filepath.Join(a.root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".stipulator-apply-*")
	if err != nil {
		return nil, nil, err
	}
	discard := func() { os.Remove(tmp.Name()) }
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		discard()
		return nil, nil, err
	}
	if err := tmp.Close(); err != nil {
		discard()
		return nil, nil, err
	}
	if create {
		// The name is reserved by an exclusive create — it fails when
		// the target exists — and the staged content then lands over
		// the reservation by the same rename every write uses, so the
		// create needs nothing of the filesystem beyond the rename.
		return func() error {
			f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if errors.Is(err, fs.ErrExist) {
				return Appeared(path)
			}
			if err != nil {
				return err
			}
			reserved, err := f.Stat()
			if err != nil {
				f.Close()
				return fmt.Errorf("%w (an empty reservation stands at %s)", err, path)
			}
			// A failure after the reservation removes it — the name is
			// absent again, as the operation read it — but only the
			// applier's own empty file, judged by identity: a foreign
			// writer landing at the name in the window keeps its file.
			// A removal that fails is named beside the fault, so an
			// empty file standing at the name is never silent.
			unreserve := func(fault error) error {
				current, err := os.Lstat(full)
				if err != nil || !os.SameFile(reserved, current) {
					return fault
				}
				if err := os.Remove(full); err != nil {
					return fmt.Errorf("%w (an empty reservation stands at %s: %v)", fault, path, err)
				}
				return fault
			}
			if err := f.Close(); err != nil {
				return unreserve(err)
			}
			if reservedForTest != nil {
				reservedForTest(full)
			}
			if err := os.Rename(tmp.Name(), full); err != nil {
				return unreserve(err)
			}
			return nil
		}, discard, nil
	}
	return func() error { return os.Rename(tmp.Name(), full) }, discard, nil
}

// reservedForTest runs between a create's reservation and its rename:
// a test's stand-in for the writer or the fault landing in that window.
var reservedForTest func(full string)

func (a *Applier) remove(path string) error {
	if a.Remove != nil {
		return a.Remove(path)
	}
	return os.Remove(filepath.Join(a.root, filepath.FromSlash(path)))
}

// Apply lands the batch (REQ-record-cas). On an error after the first
// commit the result names what landed before it.
func (a *Applier) Apply(ups []author.Update) (Result, error) {
	// One apply at a time: an unserialized check-then-write would let
	// two batches both pass their preconditions and then clobber each
	// other — the precise loss the clause exists to refuse. A
	// process-local mutex is transient in-memory state, exactly what
	// the clause sanctions.
	a.mu.Lock()
	defer a.mu.Unlock()
	fsys := a.tree()
	// Admissibility is judged for the whole batch before anything else:
	// a document the confinement refuses refuses the batch with nothing
	// written, never after the store half has landed
	// (REQ-change-retarget's all-or-nothing) — and before the
	// precondition's read, which refuses an unclean path on its own
	// terms and would hide the confinement's answer.
	seen := map[string]bool{}
	for _, up := range ups {
		// A duplicate path would pass every pre-batch precondition and
		// then last-write-wins silently; no verb produces one today, so
		// reaching this is a programming error, refused loudly.
		if seen[up.Path] {
			return Result{}, fmt.Errorf("batch names %s twice; refusing the ambiguous apply", up.Path)
		}
		seen[up.Path] = true
		if err := admitWrite(fsys, up.Path, up.Content != nil && up.Document); err != nil {
			return Result{}, err
		}
		if err := a.confinedOnDisk(up.Path, up.Content != nil && up.Document); err != nil {
			return Result{}, err
		}
	}
	for _, up := range ups {
		if err := checkPrior(fsys, up); err != nil {
			return Result{}, err
		}
	}
	type staged struct {
		path    string
		commit  func() error
		discard func()
	}
	var writes []staged
	var deletions []string
	// Any stage not committed by the time we return is discarded: a
	// leaked dot-temp is invisible to the record loader, but tidiness
	// is free.
	defer func() {
		for _, w := range writes {
			w.discard()
		}
	}()
	for _, up := range ups {
		if up.Content == nil {
			deletions = append(deletions, up.Path)
			continue
		}
		commit, discard, err := a.stage(up.Path, up.Content, up.PriorAbsent)
		if err != nil {
			return Result{}, err
		}
		writes = append(writes, staged{path: up.Path, commit: commit, discard: discard})
	}
	var out Result
	for len(writes) > 0 {
		w := writes[0]
		if err := w.commit(); err != nil {
			return out, err
		}
		writes = writes[1:]
		out.Wrote = append(out.Wrote, w.path)
	}
	for _, path := range deletions {
		if err := a.remove(path); err != nil {
			return out, err
		}
		out.Deleted = append(out.Deleted, path)
	}
	return out, nil
}

// checkPrior is one update's compare-and-swap precondition against the
// tree.
func checkPrior(fsys fs.FS, up author.Update) error {
	// An update carrying neither a prior nor read-absence was never
	// stamped: a stamped update always sets one (fs.ReadFile returns
	// non-nil even for an empty file). Refusing makes a missing stamp
	// loud at apply time instead of a silent CAS hole.
	if up.Prior == nil && !up.PriorAbsent {
		return fmt.Errorf("%s carries no precondition; the computing operation failed to stamp what it read", up.Path)
	}
	current, err := fs.ReadFile(fsys, up.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if !up.PriorAbsent && up.Prior != nil {
			return fmt.Errorf("%s vanished since the operation read it; re-run against the current tree", up.Path)
		}
		return nil
	case err != nil:
		return err
	case up.PriorAbsent:
		return Appeared(up.Path)
	case !bytes.Equal(current, up.Prior):
		return fmt.Errorf("%s changed since the operation read it (a concurrent write?); re-run against the current tree", up.Path)
	}
	return nil
}

// admitWrite is the confinement judgment (REQ-mcp-writes-confined): a
// clean local path under .stipulator/, or — for an update marked as a
// document rewrite — a document the corpus's manifest names, so a
// retarget's pointer rewrite lands in the spec document that names the
// pointer and nowhere else.
func admitWrite(fsys fs.FS, path string, document bool) error {
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return fmt.Errorf("path %q escapes the corpus root", path)
	}
	// The prefix is judged on the clean spelling only: an embedded ".."
	// would satisfy a lexical prefix check while writing outside the
	// home.
	if path != pathpkg.Clean(path) {
		return fmt.Errorf("path %q is not a clean path", path)
	}
	if strings.HasPrefix(path, ".stipulator/") {
		return nil
	}
	if !document {
		return fmt.Errorf("path %q is outside .stipulator/ (the record stores are written nowhere else)", path)
	}
	m, err := corpus.LoadManifest(fsys)
	if err != nil {
		return fmt.Errorf("document rewrite of %q: %w", path, err)
	}
	docs, err := corpus.Enumerate(fsys, m)
	if err != nil {
		return fmt.Errorf("document rewrite of %q: %w", path, err)
	}
	if !slices.Contains(docs, path) {
		return fmt.Errorf("path %q is not a corpus document (enforcement pointers are rewritten in corpus documents and nothing else)", path)
	}
	return nil
}

// confinedOnDisk judges the path's home on the tree as it stands, where
// admitWrite judges its spelling: the path's deepest existing ancestor,
// symlinks resolved, must lie under the corpus root's own `.stipulator/`
// — or under the root itself for a document rewrite — so a symlink
// pointing outside, which the rename would follow, is refused; a record
// home not yet created lands under the root. It consults the OS at the
// root — the one place the default stager writes — never the handed
// tree, so it runs only when that stager does.
func (a *Applier) confinedOnDisk(path string, document bool) error {
	if a.Stage != nil {
		return nil // an injected stager owns its placement
	}
	root := a.root
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("corpus root %s: %w", root, err)
	}
	home := filepath.Join(rootReal, ".stipulator")
	if document {
		home = rootReal
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	existing := full
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return fmt.Errorf("path %q has no existing ancestor under %s (confinement)", path, root)
		}
		existing = parent
	}
	if !document && !within(existing, filepath.Join(root, ".stipulator")) {
		// The record home itself is not there yet: the write creates it
		// under the root, which is confined exactly when the root is.
		home = rootReal
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return fmt.Errorf("path %q cannot be resolved for the confinement judgment: %w", path, err)
	}
	if !within(resolved, home) {
		return fmt.Errorf("path %q resolves to %s, outside %s (a symlink the write would follow); refusing the write", path, resolved, home)
	}
	return nil
}

// within reports whether p is base or lies under it.
func within(p, base string) bool {
	return p == base || strings.HasPrefix(p, base+string(filepath.Separator))
}
