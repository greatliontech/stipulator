package recordapply

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/stipulate"
)

func read(t *testing.T, root, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return ""
	}
	return string(b)
}

// A batch checks every admission and every precondition before its
// first write, stages every file before its first rename, creates a
// file read as absent exclusively, and deletes last: a moved target
// refuses the whole batch by name with nothing written, an
// inadmissible path likewise, a stage that fails at the second path
// leaves the first unrenamed, a deletion failing after the writes
// leaves them standing and named, and a file appearing before its
// create refuses by name (REQ-record-cas, REQ-mcp-writes-confined).
//
//gofresh:pure
func TestApplyStagesEveryFileBeforeTheFirstRename(t *testing.T) {
	stipulate.Covers(t, "REQ-record-cas", "REQ-mcp-writes-confined")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".stipulator", "gaps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".stipulator", "gaps", "a.textproto"), []byte("a0"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(root, func() fs.FS { return os.DirFS(root) })
	// A moved target refuses the batch by name; nothing lands.
	_, err := a.Apply([]author.Update{
		{Path: ".stipulator/gaps/new.textproto", Content: []byte("n"), PriorAbsent: true},
		{Path: ".stipulator/gaps/a.textproto", Content: []byte("a1"), Prior: []byte("what it read")},
	})
	if err == nil || !strings.Contains(err.Error(), ".stipulator/gaps/a.textproto changed since") || read(t, root, ".stipulator/gaps/new.textproto") != "" {
		t.Fatalf("moved target: %v; new landed %q", err, read(t, root, ".stipulator/gaps/new.textproto"))
	}
	// An inadmissible path refuses the batch before any write.
	_, err = a.Apply([]author.Update{
		{Path: ".stipulator/gaps/new.textproto", Content: []byte("n"), PriorAbsent: true},
		{Path: "notes/b.md", Content: []byte("y"), PriorAbsent: true},
	})
	if err == nil || !strings.Contains(err.Error(), "outside .stipulator/") || read(t, root, ".stipulator/gaps/new.textproto") != "" {
		t.Fatalf("inadmissible path: %v; new landed %q", err, read(t, root, ".stipulator/gaps/new.textproto"))
	}
	// A duplicate path is refused before any write.
	if _, err := a.Apply([]author.Update{
		{Path: ".stipulator/gaps/a.textproto", Content: []byte("a1"), Prior: []byte("a0")},
		{Path: ".stipulator/gaps/a.textproto", Content: []byte("a2"), Prior: []byte("a0")},
	}); err == nil || !strings.Contains(err.Error(), "twice") || read(t, root, ".stipulator/gaps/a.textproto") != "a0" {
		t.Fatalf("duplicate path: %v; a = %q", err, read(t, root, ".stipulator/gaps/a.textproto"))
	}
	// Every file stages before the first rename: a stage failing at the
	// second path leaves the first at its prior content, the staged
	// temp swept.
	staged := 0
	refused := errors.New("disk full")
	a.Stage = func(path string, content []byte, create bool) (func() error, func(), error) {
		staged++
		if staged == 2 {
			return nil, nil, refused
		}
		// The first path stages for real: the default, reached with
		// the seam cleared for the call.
		a.Stage = nil
		defer func() {
			a.Stage = func(string, []byte, bool) (func() error, func(), error) { return nil, nil, refused }
		}()
		return a.stage(path, content, create)
	}
	_, err = a.Apply([]author.Update{
		{Path: ".stipulator/gaps/a.textproto", Content: []byte("a1"), Prior: []byte("a0")},
		{Path: ".stipulator/gaps/new.textproto", Content: []byte("n"), PriorAbsent: true},
	})
	if !errors.Is(err, refused) || read(t, root, ".stipulator/gaps/a.textproto") != "a0" {
		t.Fatalf("a stage failing second: %v; a = %q (want the prior, unrenamed)", err, read(t, root, ".stipulator/gaps/a.textproto"))
	}
	temps, _ := filepath.Glob(filepath.Join(root, ".stipulator", "gaps", ".stipulator-apply-*"))
	if len(temps) != 0 {
		t.Fatalf("staged temps left behind: %v", temps)
	}
	a.Stage = nil
	// Deletions land last: a deletion failing after the writes leaves
	// every write standing and named — a duplicate the tree shows,
	// never a record lost to a deletion that landed before a failed
	// write.
	a.Remove = func(string) error { return refused }
	out, err := a.Apply([]author.Update{
		{Path: ".stipulator/gaps/a.textproto", Prior: []byte("a0")},
		{Path: ".stipulator/gaps/b.textproto", Content: []byte("b"), PriorAbsent: true},
	})
	if !errors.Is(err, refused) || strings.Join(out.Wrote, ",") != ".stipulator/gaps/b.textproto" || len(out.Deleted) != 0 || read(t, root, ".stipulator/gaps/a.textproto") != "a0" || read(t, root, ".stipulator/gaps/b.textproto") != "b" {
		t.Fatalf("a deletion failing after the writes = %+v, %v; a = %q, b = %q", out, err, read(t, root, ".stipulator/gaps/a.textproto"), read(t, root, ".stipulator/gaps/b.textproto"))
	}
	if out.Landed() != "landed before the fault: wrote .stipulator/gaps/b.textproto" {
		t.Fatalf("Landed() = %q", out.Landed())
	}
	a.Remove = nil
	if err := os.Remove(filepath.Join(root, ".stipulator", "gaps", "b.textproto")); err != nil {
		t.Fatal(err)
	}
	// A commit failing after another landed names the landed one.
	var failingD func(path string, content []byte, create bool) (func() error, func(), error)
	failingD = func(path string, content []byte, create bool) (func() error, func(), error) {
		a.Stage = nil
		defer func() { a.Stage = failingD }()
		commit, discard, err := a.stage(path, content, create)
		if path == ".stipulator/gaps/d.textproto" {
			commit = func() error { return refused }
		}
		return commit, discard, err
	}
	a.Stage = failingD
	out, err = a.Apply([]author.Update{
		{Path: ".stipulator/gaps/b.textproto", Content: []byte("b"), PriorAbsent: true},
		{Path: ".stipulator/gaps/d.textproto", Content: []byte("d"), PriorAbsent: true},
	})
	if !errors.Is(err, refused) || strings.Join(out.Wrote, ",") != ".stipulator/gaps/b.textproto" || read(t, root, ".stipulator/gaps/b.textproto") != "b" || read(t, root, ".stipulator/gaps/d.textproto") != "" {
		t.Fatalf("a commit failing second = %+v, %v; b = %q, d = %q", out, err, read(t, root, ".stipulator/gaps/b.textproto"), read(t, root, ".stipulator/gaps/d.textproto"))
	}
	a.Stage = nil
	if err := os.Remove(filepath.Join(root, ".stipulator", "gaps", "b.textproto")); err != nil {
		t.Fatal(err)
	}
	// A file read as absent is created exclusively: one appearing
	// between the precondition and the commit fails the reservation,
	// the appeared content untouched, the staged temp swept.
	a.Stage = func(path string, content []byte, create bool) (func() error, func(), error) {
		a.Stage = nil
		defer func() { a.Stage = nil }()
		commit, discard, err := a.stage(path, content, create)
		if err != nil {
			return nil, nil, err
		}
		return func() error {
			if path == ".stipulator/gaps/c.textproto" {
				if err := os.WriteFile(filepath.Join(root, ".stipulator", "gaps", "c.textproto"), []byte("appeared"), 0o644); err != nil {
					return err
				}
			}
			return commit()
		}, discard, nil
	}
	if _, err := a.Apply([]author.Update{{Path: ".stipulator/gaps/c.textproto", Content: []byte("c"), PriorAbsent: true}}); err == nil || !strings.Contains(err.Error(), "c.textproto appeared since the operation ran") || read(t, root, ".stipulator/gaps/c.textproto") != "appeared" {
		t.Fatalf("a file appearing before its exclusive create: %v; c = %q (want the appeared content untouched)", err, read(t, root, ".stipulator/gaps/c.textproto"))
	}
	if temps, _ := filepath.Glob(filepath.Join(root, ".stipulator", "gaps", ".stipulator-apply-*")); len(temps) != 0 {
		t.Fatalf("staged temps left behind after the refused create: %v", temps)
	}
	if err := os.Remove(filepath.Join(root, ".stipulator", "gaps", "c.textproto")); err != nil {
		t.Fatal(err)
	}
	// A rename failing over the reservation removes it: the name is
	// absent again, as the operation read it — never an empty file
	// standing where a record was expected. The fault lands in the
	// window: the staged temp vanishes, so the rename fails.
	sweepTemps := func(string) {
		temps, _ := filepath.Glob(filepath.Join(root, ".stipulator", "gaps", ".stipulator-apply-*"))
		for _, tmp := range temps {
			if err := os.Remove(tmp); err != nil {
				t.Fatal(err)
			}
		}
	}
	reservedForTest = sweepTemps
	t.Cleanup(func() { reservedForTest = nil })
	if _, err := a.Apply([]author.Update{{Path: ".stipulator/gaps/e.textproto", Content: []byte("e"), PriorAbsent: true}}); err == nil || !strings.Contains(err.Error(), "e.textproto") {
		t.Fatalf("a rename failing over the reservation: %v; want an error naming the path", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".stipulator", "gaps", "e.textproto")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the reservation stands after its rename failed: %v", err)
	}
	// The removal is by identity: a foreign file replacing the
	// reservation in the window keeps its content, and the fault is
	// the rename's alone.
	reservedForTest = func(full string) {
		sweepTemps(full)
		if err := os.Remove(full); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("foreign"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Apply([]author.Update{{Path: ".stipulator/gaps/e.textproto", Content: []byte("e"), PriorAbsent: true}}); err == nil || strings.Contains(err.Error(), "reservation stands") || read(t, root, ".stipulator/gaps/e.textproto") != "foreign" {
		t.Fatalf("a foreign file in the window: %v; e = %q (want the foreign content kept)", err, read(t, root, ".stipulator/gaps/e.textproto"))
	}
	if err := os.Remove(filepath.Join(root, ".stipulator", "gaps", "e.textproto")); err != nil {
		t.Fatal(err)
	}
	// A removal that fails is named beside the fault: the directory
	// made read-only in the window refuses the rename and the removal
	// alike.
	reservedForTest = func(full string) {
		sweepTemps(full)
		if err := os.Chmod(filepath.Dir(full), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	_, err = a.Apply([]author.Update{{Path: ".stipulator/gaps/e.textproto", Content: []byte("e"), PriorAbsent: true}})
	if chmodErr := os.Chmod(filepath.Join(root, ".stipulator", "gaps"), 0o755); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	if err == nil || !strings.Contains(err.Error(), "an empty reservation stands at .stipulator/gaps/e.textproto") || read(t, root, ".stipulator/gaps/e.textproto") != "" {
		t.Fatalf("a removal failing after the rename: %v; e = %q", err, read(t, root, ".stipulator/gaps/e.textproto"))
	}
	if _, err := os.Stat(filepath.Join(root, ".stipulator", "gaps", "e.textproto")); err != nil {
		t.Fatalf("the reservation the removal could not remove is gone: %v", err)
	}
	if err := os.Remove(filepath.Join(root, ".stipulator", "gaps", "e.textproto")); err != nil {
		t.Fatal(err)
	}
	reservedForTest = nil
	// A good batch lands every file, then deletes, in batch order; a
	// created file leaves no temp beside it.
	out, err = a.Apply([]author.Update{
		{Path: ".stipulator/gaps/new.textproto", Content: []byte("n"), PriorAbsent: true},
		{Path: ".stipulator/gaps/a.textproto", Prior: []byte("a0")},
		{Path: ".stipulator/gaps/z.textproto", Content: []byte("z"), PriorAbsent: true},
	})
	if err != nil || strings.Join(out.Wrote, ",") != ".stipulator/gaps/new.textproto,.stipulator/gaps/z.textproto" || strings.Join(out.Deleted, ",") != ".stipulator/gaps/a.textproto" {
		t.Fatalf("good batch = %+v, %v", out, err)
	}
	if read(t, root, ".stipulator/gaps/new.textproto") != "n" || read(t, root, ".stipulator/gaps/a.textproto") != "" {
		t.Fatal("the good batch did not land as ordered")
	}
	if temps, _ := filepath.Glob(filepath.Join(root, ".stipulator", "gaps", ".stipulator-apply-*")); len(temps) != 0 {
		t.Fatalf("temps left beside created files: %v", temps)
	}
	// A missing stamp is refused loudly.
	if _, err := a.Apply([]author.Update{{Path: ".stipulator/gaps/q.textproto", Content: []byte("q")}}); err == nil || !strings.Contains(err.Error(), "carries no precondition") {
		t.Fatalf("unstamped update: %v", err)
	}
}

// The confinement is judged on the tree as it stands, not on the
// path's spelling alone: a symlinked directory under the record home
// pointing outside the corpus root is refused before anything is
// staged, where the lexical admission would have let the rename follow
// it out; a link resolving inside the home is admitted, and a record
// home not yet created lands under the root
// (REQ-mcp-writes-confined, REQ-record-cas).
func TestApplyRefusesAPathResolvingOutsideTheRoot(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-writes-confined")
	stipulate.Covers(t, "REQ-record-cas")
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".stipulator", "gaps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".stipulator", "exports")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, ".stipulator", "gaps"), filepath.Join(root, ".stipulator", "inside")); err != nil {
		t.Fatal(err)
	}
	a := New(root, func() fs.FS { return os.DirFS(root) })
	_, err := a.Apply([]author.Update{{Path: ".stipulator/exports/x.json", Content: []byte("x"), PriorAbsent: true}})
	if err == nil || !strings.Contains(err.Error(), "resolves to") || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("symlinked home admitted: %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("the write followed the link out of the root: %v", entries)
	}
	// A sibling of the home sharing its name as a prefix is outside it.
	sibling := filepath.Join(root, ".stipulator-side")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sibling, filepath.Join(root, ".stipulator", "side")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply([]author.Update{{Path: ".stipulator/side/s.textproto", Content: []byte("s"), PriorAbsent: true}}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("prefix-sharing sibling admitted: %v", err)
	}
	if _, err := a.Apply([]author.Update{{Path: ".stipulator/inside/y.textproto", Content: []byte("y"), PriorAbsent: true}}); err != nil || read(t, root, ".stipulator/gaps/y.textproto") != "y" {
		t.Fatalf("link resolving inside the home refused or not landed: %v", err)
	}
	fresh := t.TempDir()
	b := New(fresh, func() fs.FS { return os.DirFS(fresh) })
	if _, err := b.Apply([]author.Update{{Path: ".stipulator/gaps/z.textproto", Content: []byte("z"), PriorAbsent: true}}); err != nil || read(t, fresh, ".stipulator/gaps/z.textproto") != "z" {
		t.Fatalf("a record home not yet created refused: %v", err)
	}
}

// The on-disk confinement judges the OS at the root only when the
// default stager writes there: an injected stager owns its placement,
// so a harness landing writes in a handed tree under a root that does
// not exist on disk is admitted — the seam, not the OS, is the tree
// (REQ-mcp-writes-confined).
func TestInjectedStagerOwnsItsPlacement(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-writes-confined")
	tree := fstest.MapFS{}
	a := New(filepath.Join(t.TempDir(), "nonexistent"), func() fs.FS { return tree })
	staged := map[string]string{}
	a.Stage = func(path string, content []byte, create bool) (func() error, func(), error) {
		return func() error { staged[path] = string(content); return nil }, func() {}, nil
	}
	if _, err := a.Apply([]author.Update{{Path: ".stipulator/gaps/g.textproto", Content: []byte("g"), PriorAbsent: true}}); err != nil || staged[".stipulator/gaps/g.textproto"] != "g" {
		t.Fatalf("injected stager under a root absent on disk: %v, staged %v; want the write landed through the seam", err, staged)
	}
}
