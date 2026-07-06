package gitfs

import (
	"io/fs"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/diff"
	"github.com/greatliontech/stipulator/stipulate"
)

func osDirFS(dir string) fs.FS { return os.DirFS(dir) }

// repoWith builds a real repository in t.TempDir with one commit holding
// the given files, then applies worktree edits uncommitted.
func repoWith(t *testing.T, committed, worktree map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	write := func(files map[string]string) {
		for p, c := range files {
			full := wt.Filesystem
			if i := strings.LastIndex(p, "/"); i >= 0 {
				if err := full.MkdirAll(p[:i], 0o755); err != nil {
					t.Fatal(err)
				}
			}
			f, err := full.Create(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write([]byte(c)); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
	write(committed)
	if _, err := wt.Add("."); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("corpus v1", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t", When: time.Unix(0, 0)},
	}); err != nil {
		t.Fatal(err)
	}
	write(worktree)
	return dir
}

// TestRevisionCorpusDiff pins the diff-against-revision path end to end:
// the committed corpus compiles from the object store — no checkout — and
// diffs against the edited working tree.
func TestRevisionCorpusDiff(t *testing.T) {
	stipulate.Covers(t, "REQ-change-diff-revision")
	man := ".stipulator/manifest.textproto"
	dir := repoWith(t,
		map[string]string{
			man:          "include: \"specs/**/*.md\"\n",
			"specs/a.md": "# T\n\n**REQ-g-a** (behavior): It MUST x.\n",
		},
		map[string]string{
			"specs/a.md": "# T\n\n**REQ-g-a** (behavior): It MUST x differently.\n\n**REQ-g-b** (behavior): It MUST y.\n",
		})

	fsys, err := FS(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	oldSpec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile at revision: %v %v", err, diags)
	}
	if n := len(oldSpec.GetRequirements()); n != 1 {
		t.Fatalf("revision corpus requirements = %d", n)
	}

	// The working tree edits are invisible at the revision.
	b, err := fs.ReadFile(fsys, "specs/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "differently") {
		t.Fatal("revision read reflects the working tree")
	}

	newSpec, diags, err := compile.Compile(osDirFS(dir))
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile worktree: %v %v", err, diags)
	}
	r := diff.Diff(oldSpec, newSpec)
	if len(r.AddedRequirements) != 1 || r.AddedRequirements[0] != "REQ-g-b" {
		t.Fatalf("added = %v", r.AddedRequirements)
	}
	if len(r.TextChangedRequirements) != 1 || r.TextChangedRequirements[0] != "REQ-g-a" {
		t.Fatalf("text-changed = %v", r.TextChangedRequirements)
	}

	// Unknown revision: loud.
	if _, err := FS(dir, "no-such-rev"); err == nil || !strings.Contains(err.Error(), "resolving revision") {
		t.Fatalf("unknown revision accepted: %v", err)
	}
}

// TestRevisionSubdirCorpus pins prefix mapping: a corpus root below the
// repository root maps onto the same subtree at the revision.
func TestRevisionSubdirCorpus(t *testing.T) {
	stipulate.Covers(t, "REQ-change-diff-revision")
	dir := repoWith(t,
		map[string]string{
			"proj/.stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
			"proj/specs/a.md":                     "# T\n\n**REQ-g-a** (behavior): It MUST x.\n",
		}, nil)
	fsys, err := FS(dir+"/proj", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	if len(spec.GetRequirements()) != 1 {
		t.Fatalf("requirements = %+v", spec.GetRequirements())
	}
	// Outside the repository: refused.
	if _, err := FS(t.TempDir(), "HEAD"); err == nil {
		t.Fatal("non-repository accepted")
	}
}

// TestLinkedWorktreeRefused pins the fail-loud contract: the embedded git
// opens a linked worktree's gitfile redirection but resolves no
// references, so gitfs refuses with the real cause instead of blaming
// the revision.
func TestLinkedWorktreeRefused(t *testing.T) {
	stipulate.Covers(t, "REQ-change-diff-revision")
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := repoWith(t, map[string]string{"a.txt": "x\n"}, nil)
	linked := t.TempDir() + "/wt"
	out, err := exec.Command("git", "-C", dir, "worktree", "add", linked).CombinedOutput()
	if err != nil {
		t.Skipf("git worktree add failed: %v: %s", err, out)
	}
	_, err = FS(linked, "HEAD")
	if err == nil || !strings.Contains(err.Error(), "linked git worktree") {
		t.Fatalf("linked worktree not refused with the real cause: %v", err)
	}
}

// TestSymlinkEntriesRefused pins the symlink contract: a symlinked
// corpus file at a revision is refused loudly — serving the link-target
// path as content would silently compile it as spec text — and directory
// listings report the honest entry type.
func TestSymlinkEntriesRefused(t *testing.T) {
	stipulate.Covers(t, "REQ-change-diff-revision")
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	f, err := wt.Filesystem.Create("real.md")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("# T\n"))
	f.Close()
	if err := wt.Filesystem.Symlink("real.md", "link.md"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := wt.Add("."); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("v1", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t", When: time.Unix(0, 0)},
	}); err != nil {
		t.Fatal(err)
	}
	fsys, err := FS(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadFile(fsys, "link.md"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink served as content: %v", err)
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
		if e.Name() == "link.md" && e.Type() != fs.ModeSymlink {
			t.Fatalf("symlink entry typed %v", e.Type())
		}
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("ReadDir not name-sorted: %v", names)
	}
}
