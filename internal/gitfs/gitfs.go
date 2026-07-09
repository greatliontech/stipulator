// Package gitfs serves a git commit's tree as an fs.FS, straight from the
// repository's object store: no checkout, no worktree mutation, no
// temporary files. It exists so diff can compile a corpus as it was at a
// committed revision (REQ-change-diff-revision).
package gitfs

import (
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// FS resolves rev in the repository containing dir (discovered by
// walking upward for .git; GIT_DIR-style environment overrides are
// deliberately not consulted) and returns the commit's tree as an fs.FS
// rooted at dir's repo-relative prefix — so the caller's corpus root maps
// onto the same corpus root at the revision. The revision accepts
// anything git rev-parse does (HEAD~2, branch, tag, hash).
func FS(dir, rev string) (fs.FS, error) {
	// EnableDotGitCommonDir wires the commondir indirection, so linked
	// worktrees (git worktree add — .git is a file) resolve their
	// references and objects; without it every revision reads as
	// "reference not found".
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true, EnableDotGitCommonDir: true})
	if err != nil {
		return nil, fmt.Errorf("opening git repository at %s: %w", dir, err)
	}
	hash, err := repo.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return nil, fmt.Errorf("resolving revision %q: %w", rev, err)
	}
	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, fmt.Errorf("reading commit %s: %w", hash, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("reading tree of %s: %w", hash, err)
	}
	fsys := &treeFS{tree: tree, when: commit.Committer.When}

	prefix, err := repoRelative(repo, dir)
	if err != nil {
		return nil, err
	}
	if prefix == "" {
		return fsys, nil
	}
	sub, err := fs.Sub(fsys, prefix)
	if err != nil {
		return nil, fmt.Errorf("revision %q has no directory %q: %w", rev, prefix, err)
	}
	return sub, nil
}

// Changed returns the paths, relative to dir and slash-separated, of every
// file under dir whose working-tree state differs from HEAD — modified,
// added, staged, untracked, or deleted alike. It is the staged-delta
// boundary the harden classification measures against (REQ-harden-staged-
// scope): "the working tree against HEAD" is the whole change set since the
// last commit, whether or not the operator has run `git add`. Paths outside
// dir's subtree (the corpus root's repo-relative prefix) are excluded, so a
// nested corpus never sees sibling changes. Deleted paths are included: their
// symbols vanished from the delta and the classifier resolves them to no
// bound body.
func Changed(dir string) ([]string, error) {
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true, EnableDotGitCommonDir: true})
	if err != nil {
		return nil, fmt.Errorf("opening git repository at %s: %w", dir, err)
	}
	prefix, err := repoRelative(repo, dir)
	if err != nil {
		return nil, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("reading worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("reading worktree status: %w", err)
	}
	var out []string
	for p, st := range status {
		if st.Staging == git.Unmodified && st.Worktree == git.Unmodified {
			continue
		}
		rel := p // status paths are repo-root-relative, slash-separated
		if prefix != "" {
			if rel != prefix && !strings.HasPrefix(rel, prefix+"/") {
				continue
			}
			rel = strings.TrimPrefix(rel, prefix+"/")
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, nil
}

// repoRelative returns dir's clean path relative to the repository's
// worktree root, "" for the root itself.
func repoRelative(repo *git.Repository, dir string) (string, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("bare repositories carry no corpus root context: %w", err)
	}
	root := wt.Filesystem.Root()
	abs, err := absPath(dir)
	if err != nil {
		return "", err
	}
	rootAbs, err := absPath(root)
	if err != nil {
		return "", err
	}
	if abs == rootAbs {
		return "", nil
	}
	if !strings.HasPrefix(abs, rootAbs+"/") {
		return "", fmt.Errorf("%s is outside the repository %s", dir, rootAbs)
	}
	return strings.TrimPrefix(abs, rootAbs+"/"), nil
}

// treeFS adapts an object.Tree to fs.FS. Paths are slash-separated and
// relative, per fs.ValidPath.
type treeFS struct {
	tree *object.Tree
	when time.Time
}

func (t *treeFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return &treeDir{fs: t, path: ".", entries: sortedEntries(t.tree.Entries)}, nil
	}
	entry, err := t.tree.FindEntry(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if entry.Mode == filemode.Dir {
		sub, err := t.tree.Tree(name)
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}
		return &treeDir{fs: t, path: name, entries: sortedEntries(sub.Entries)}, nil
	}
	if entry.Mode == filemode.Symlink {
		// Serving the link-target path as file content would compile it
		// as spec text — refuse loudly instead.
		return nil, &fs.PathError{Op: "open", Path: name, Err: fmt.Errorf("symlink entries are not served from a revision; commit the real file")}
	}
	file, err := t.tree.File(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	r, err := file.Reader()
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return &treeFile{name: path.Base(name), size: file.Size, when: t.when, r: r}, nil
}

func (t *treeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	f, err := t.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d, ok := f.(*treeDir)
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	return d.ReadDir(-1)
}

// treeFile is one blob, opened.
type treeFile struct {
	name string
	size int64
	when time.Time
	r    io.ReadCloser
}

func (f *treeFile) Stat() (fs.FileInfo, error) {
	return fileInfo{name: f.name, size: f.size, when: f.when}, nil
}
func (f *treeFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *treeFile) Close() error               { return f.r.Close() }

// treeDir is one tree, opened as a directory.
type treeDir struct {
	fs      *treeFS
	path    string
	entries []object.TreeEntry
	offset  int
}

func (d *treeDir) Stat() (fs.FileInfo, error) {
	return fileInfo{name: path.Base(d.path), dir: true, when: d.fs.when}, nil
}
func (d *treeDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: fs.ErrInvalid}
}
func (d *treeDir) Close() error { return nil }

func (d *treeDir) ReadDir(n int) ([]fs.DirEntry, error) {
	entries := d.entries[d.offset:]
	if n > 0 && len(entries) > n {
		entries = entries[:n]
	}
	d.offset += len(entries)
	if n > 0 && len(entries) == 0 {
		return nil, io.EOF
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, dirEntry{fs: d.fs, dir: d.path, entry: e})
	}
	return out, nil
}

// dirEntry is one tree entry listed by ReadDir.
type dirEntry struct {
	fs    *treeFS
	dir   string
	entry object.TreeEntry
}

func (e dirEntry) Name() string { return e.entry.Name }
func (e dirEntry) IsDir() bool  { return e.entry.Mode == filemode.Dir }
func (e dirEntry) Type() fs.FileMode {
	switch e.entry.Mode {
	case filemode.Dir:
		return fs.ModeDir
	case filemode.Symlink:
		return fs.ModeSymlink
	}
	return 0
}

// sortedEntries copies and name-sorts tree entries: git's tree order
// sorts directories as name+"/", which violates fs.ReadDir's sorted
// contract on prefix-name edges.
func sortedEntries(entries []object.TreeEntry) []object.TreeEntry {
	out := make([]object.TreeEntry, len(entries))
	copy(out, entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (e dirEntry) Info() (fs.FileInfo, error) {
	if e.IsDir() {
		return fileInfo{name: e.entry.Name, dir: true, when: e.fs.when}, nil
	}
	full := e.entry.Name
	if e.dir != "." {
		full = e.dir + "/" + e.entry.Name
	}
	f, err := e.fs.tree.File(full)
	if err != nil {
		return nil, err
	}
	return fileInfo{name: e.entry.Name, size: f.Size, when: e.fs.when}, nil
}

// fileInfo is the minimal FileInfo both shapes share; times are the
// commit's, the only timestamp a git tree carries.
type fileInfo struct {
	name string
	size int64
	dir  bool
	when time.Time
}

func (i fileInfo) Name() string { return i.name }
func (i fileInfo) Size() int64  { return i.size }
func (i fileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i fileInfo) ModTime() time.Time { return i.when }
func (i fileInfo) IsDir() bool        { return i.dir }
func (i fileInfo) Sys() any           { return nil }

// absPath is filepath.Abs, isolated for the one os dependency.
var absPath = defaultAbs

func defaultAbs(p string) (string, error) { return filepath.Abs(p) }
