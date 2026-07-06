package index

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/stipulate"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func compileFixture(t *testing.T, files map[string]string) (*stipulatorv1.Spec, fstest.MapFS) {
	t.Helper()
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
	}
	for p, c := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(c)}
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	return spec, fsys
}

const docA = "# Alpha contract\n\n**REQ-ix-a** (behavior): It MUST a.\n\n**term one** (term): a thing.\n"
const docB = "# Beta contract\n\n**REQ-ix-b** (invariant): It MUST NOT b.\n"

// TestBuild pins the generated-index shape: one README per corpus
// directory, marker present, every document and requirement listed, and no
// normative text — requirement bodies never appear.
func TestBuild(t *testing.T) {
	stipulate.Covers(t, "REQ-profile-index-generated")
	spec, _ := compileFixture(t, map[string]string{
		"specs/a.md":     docA,
		"specs/z.md":     "# Zeta terms\n\n**zterm** (term): only terms here.\n",
		"specs/sub/b.md": docB,
	})
	got := Build(spec)
	if len(got) != 2 {
		t.Fatalf("indexes = %d: %v", len(got), got)
	}
	top := string(got["specs/README.md"])
	sub := string(got["specs/sub/README.md"])
	for _, idx := range []string{top, sub} {
		if !strings.Contains(idx, Marker) {
			t.Fatalf("marker missing:\n%s", idx)
		}
	}
	if !strings.Contains(top, "| Document | Title | Requirements | Terms |") {
		t.Fatalf("table header missing:\n%s", top)
	}
	if !strings.Contains(top, "| [a.md](a.md) | Alpha contract | 1 | 1 |") ||
		!strings.Contains(top, "| [z.md](z.md) | Zeta terms | 0 | 1 |") ||
		!strings.Contains(top, "`REQ-ix-a` (behavior, must)") {
		t.Fatalf("top index incomplete:\n%s", top)
	}
	// Documents list in path order.
	if strings.Index(top, "[a.md]") > strings.Index(top, "[z.md]") {
		t.Fatalf("documents out of order:\n%s", top)
	}
	// Per-document sections exist for requirement-bearing documents only.
	if !strings.Contains(top, "## a.md") {
		t.Fatalf("section heading missing:\n%s", top)
	}
	if strings.Contains(top, "## z.md") {
		t.Fatalf("requirement-less document grew a section:\n%s", top)
	}
	if !strings.Contains(sub, "`REQ-ix-b` (invariant, must not)") {
		t.Fatalf("sub index incomplete:\n%s", sub)
	}
	// No normative text: the requirement body never appears.
	if strings.Contains(top, "It MUST a") {
		t.Fatalf("normative text leaked into the index:\n%s", top)
	}
}

// TestStale pins the freshness lint: absent, current, drifted, and the
// unmarked-README refusal with force override.
func TestStale(t *testing.T) {
	stipulate.Covers(t, "REQ-profile-index-fresh")
	spec, fsys := compileFixture(t, map[string]string{"specs/a.md": docA, "specs/sub/b.md": docB})

	stale, err := Stale(fsys, spec, false)
	if err != nil || len(stale) != 2 || stale[0] != "specs/README.md" || stale[1] != "specs/sub/README.md" {
		t.Fatalf("absent indexes not stale in order: %v %v", stale, err)
	}
	fsys["specs/sub/README.md"] = &fstest.MapFile{Data: Build(spec)["specs/sub/README.md"]}

	fsys["specs/README.md"] = &fstest.MapFile{Data: Build(spec)["specs/README.md"]}
	if stale, err = Stale(fsys, spec, false); err != nil || len(stale) != 0 {
		t.Fatalf("current index read stale: %v %v", stale, err)
	}

	fsys["specs/README.md"] = &fstest.MapFile{Data: []byte("# Index — specs\n\n" + Marker + "\n\nold\n")}
	if stale, err = Stale(fsys, spec, false); err != nil || len(stale) != 1 {
		t.Fatalf("drifted index not stale: %v %v", stale, err)
	}

	fsys["specs/README.md"] = &fstest.MapFile{Data: []byte("# Hand-written\n")}
	if _, err = Stale(fsys, spec, false); err == nil || !strings.Contains(err.Error(), "not a generated index") {
		t.Fatalf("unmarked README not refused: %v", err)
	}
	if stale, err = Stale(fsys, spec, true); err != nil || len(stale) != 1 {
		t.Fatalf("force did not override: %v %v", stale, err)
	}
}
