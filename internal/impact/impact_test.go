package impact

import (
	"context"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/greatliontech/stipulator/stipulate"
)

// repoWith builds a real repository in t.TempDir with one commit holding
// the committed files, then applies worktree edits uncommitted.
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
	if _, err := wt.Commit("v1", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t", When: time.Unix(0, 0)},
	}); err != nil {
		t.Fatal(err)
	}
	write(worktree)
	return dir
}

// fixture is a corpus + module with two distinct reach routes to the
// leaf — mid imports it in production code, other only from an external
// test package — and an island with no route at all: the reach boundary
// and the test-variant fold, in one tree.
var fixture = map[string]string{
	".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
	"specs/spec.md": "# Spec\n\n" +
		"**REQ-imp-leaf** (behavior): Leaf MUST double.\n\n" +
		"**REQ-imp-mid** (behavior): Mid MUST quadruple.\n\n" +
		"**REQ-imp-other** (behavior): Other MUST hold.\n\n" +
		"**REQ-imp-island** (behavior): Island MUST stand.\n",
	"go.mod":       "module example.com/imp\n\ngo 1.26.4\n",
	"leaf/leaf.go": "package leaf\n\nfunc Double(x int) int { return 2 * x }\n",
	"mid/mid.go": "package mid\n\nimport \"example.com/imp/leaf\"\n\n" +
		"func Quad(x int) int { return leaf.Double(leaf.Double(x)) }\n",
	"mid/mid_test.go": "package mid\n\nimport \"testing\"\n\n" +
		"func TestQuad(t *testing.T) { Quad(1) }\n",
	"other/other.go": "package other\n\nfunc Hold() bool { return true }\n",
	"other/other_test.go": "package other_test\n\nimport (\n\t\"testing\"\n\n" +
		"\t\"example.com/imp/leaf\"\n\t\"example.com/imp/other\"\n)\n\n" +
		"func TestHold(t *testing.T) {\n\tif !other.Hold() || leaf.Double(1) != 2 {\n\t\tt.Fail()\n\t}\n}\n",
	"island/island.go": "package island\n\nfunc Stand() bool { return true }\n",
	"island/island_test.go": "package island\n\nimport \"testing\"\n\n" +
		"func TestStand(t *testing.T) { Stand() }\n",
	".stipulator/bindings/all.textproto": "" +
		"bindings { requirement_id: \"REQ-imp-leaf\" backend: \"go\" symbol: \"example.com/imp/leaf.Double\" role: BINDING_ROLE_IMPLEMENTS }\n" +
		"bindings { requirement_id: \"REQ-imp-mid\" backend: \"go\" symbol: \"example.com/imp/mid.TestQuad\" role: BINDING_ROLE_TESTS }\n" +
		"bindings { requirement_id: \"REQ-imp-other\" backend: \"go\" symbol: \"example.com/imp/other.TestHold\" role: BINDING_ROLE_TESTS }\n" +
		"bindings { requirement_id: \"REQ-imp-island\" backend: \"go\" symbol: \"example.com/imp/island.TestStand\" role: BINDING_ROLE_TESTS }\n",
}

// A leaf-package edit previews the leaf's implements binding as bound in
// a changed file and reaches the mid witness (production import) and the
// other witness (its external test package imports the leaf, and the
// test variant folds onto its production package) — while the island
// witness stays out: reach is the import closure, not the module.
//
//gofresh:pure
func TestPreviewJoinsBindingsAndImportReach(t *testing.T) {
	stipulate.Covers(t, "REQ-change-impact")
	dir := repoWith(t, fixture, map[string]string{
		"leaf/leaf.go": "package leaf\n\nfunc Double(x int) int { return x + x }\n",
	})
	r, err := Preview(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changed) != 1 || r.Changed[0] != "leaf/leaf.go" {
		t.Fatalf("Changed = %v", r.Changed)
	}
	if !r.Spec.SemanticallyEmpty() {
		t.Errorf("code-only edit reports a spec delta: %v", r.Spec.Lines())
	}
	if len(r.Bound) != 1 || r.Bound[0].Requirement != "REQ-imp-leaf" ||
		r.Bound[0].File != "leaf/leaf.go" {
		t.Fatalf("Bound = %+v", r.Bound)
	}
	var got []string
	for _, w := range r.Witnesses {
		got = append(got, w.Symbol)
	}
	want := []string{"example.com/imp/mid.TestQuad", "example.com/imp/other.TestHold"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Witnesses = %v, want %v (island.TestStand must stay outside the reach)", got, want)
	}
}

// A spec-text edit previews as a per-identity semantic delta with no
// bound or reached candidates: nothing in the code moved, and the
// preview never converts a content-hash delta into a staleness verdict.
// The delta's orientation is HEAD-to-worktree — a requirement the edit
// introduces reads as added, never removed.
//
//gofresh:pure
func TestPreviewNamesSpecDeltaWithoutVerdict(t *testing.T) {
	stipulate.Covers(t, "REQ-change-impact")
	dir := repoWith(t, fixture, map[string]string{
		"specs/spec.md": "# Spec\n\n" +
			"**REQ-imp-leaf** (behavior): Leaf MUST double exactly.\n\n" +
			"**REQ-imp-mid** (behavior): Mid MUST quadruple.\n\n" +
			"**REQ-imp-other** (behavior): Other MUST hold.\n\n" +
			"**REQ-imp-island** (behavior): Island MUST stand.\n\n" +
			"**REQ-imp-new** (behavior): New MUST arrive.\n",
	})
	r, err := Preview(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Spec.TextChangedRequirements; len(got) != 1 || got[0] != "REQ-imp-leaf" {
		t.Fatalf("TextChanged = %v", got)
	}
	if got := r.Spec.AddedRequirements; len(got) != 1 || got[0] != "REQ-imp-new" {
		t.Fatalf("Added = %v (a worktree-introduced requirement must read as added, not removed)", got)
	}
	if touched := r.SpecTouched(); len(touched) != 2 {
		t.Fatalf("SpecTouched = %v", touched)
	}
	if len(r.Bound) != 0 || len(r.Witnesses) != 0 {
		t.Fatalf("spec-only edit previews code candidates: bound=%v witnesses=%v", r.Bound, r.Witnesses)
	}
}

// A corpus with no Go bindings loads no Go tree at all: the fixture's
// malformed go.work makes any attempted package load fail before its
// first go list, so this passing proves no load ran — symbol resolution
// requires nothing here, and the spec delta previews without a
// toolchain in sight (REQ-change-impact's loading bound).
//
//gofresh:pure
func TestPreviewSpecOnlyCorpusNeedsNoGoTree(t *testing.T) {
	stipulate.Covers(t, "REQ-change-impact")
	dir := repoWith(t, map[string]string{
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		"specs/spec.md":                  "# Spec\n\n**REQ-only-spec** (behavior): It MUST hold.\n",
		"go.work":                        "use (\n",
	}, map[string]string{
		"specs/spec.md": "# Spec\n\n**REQ-only-spec** (behavior): It MUST hold tightly.\n",
	})
	r, err := Preview(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Spec.TextChangedRequirements; len(got) != 1 || got[0] != "REQ-only-spec" {
		t.Fatalf("TextChanged = %v", got)
	}
}

// A clean tree loads no Go tree even when Go bindings exist: the
// code-side candidates derive solely from the change set, so an empty
// change set requires no resolution. The fixture's malformed go.work
// makes any attempted package load fail before its first go list, so
// this passing proves no load ran.
//
//gofresh:pure
func TestPreviewCleanGoBoundTreeSkipsGoLoad(t *testing.T) {
	stipulate.Covers(t, "REQ-change-impact")
	dir := repoWith(t, map[string]string{
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		"specs/spec.md":                  "# Spec\n\n**REQ-clean-go** (behavior): It MUST hold.\n",
		"go.work":                        "use (\n",
		".stipulator/bindings/all.textproto": "bindings { requirement_id: \"REQ-clean-go\" " +
			"backend: \"go\" symbol: \"example.com/gone.TestHold\" role: BINDING_ROLE_TESTS }\n",
	}, nil)
	r, err := Preview(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changed) != 0 || len(r.Bound) != 0 || len(r.Witnesses) != 0 {
		t.Fatalf("clean go-bound tree previews impact: %+v", r)
	}
}

// A clean tree previews empty everywhere; a tree outside any repository
// reports that plainly instead of guessing (REQ-change-impact).
//
//gofresh:pure
func TestPreviewCleanTreeAndNonRepo(t *testing.T) {
	stipulate.Covers(t, "REQ-change-impact")
	dir := repoWith(t, fixture, nil)
	r, err := Preview(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changed) != 0 || len(r.Bound) != 0 || len(r.Witnesses) != 0 || !r.Spec.SemanticallyEmpty() {
		t.Fatalf("clean tree previews impact: %+v", r)
	}
	if _, err := Preview(context.Background(), t.TempDir()); err == nil {
		t.Fatal("a tree outside any repository previewed without error")
	}
}
