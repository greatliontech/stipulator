package diff

import (
	"slices"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
)

func compileFiles(t *testing.T, files map[string]string) *stipulatorv1.Spec {
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
	return spec
}

func TestDiff(t *testing.T) {
	base := compileFiles(t, map[string]string{
		"specs/a.md": "# T\n\n**widget** (term): a gadget.\n\n" +
			"**REQ-d-a** (behavior): Using the widget it MUST x.\n\n" +
			"**REQ-d-b** (behavior, refines REQ-d-a): It MUST y.\n",
	})

	t.Run("identical is fully empty", func(t *testing.T) {
		same := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**widget** (term): a gadget.\n\n" +
				"**REQ-d-a** (behavior): Using the widget it MUST x.\n\n" +
				"**REQ-d-b** (behavior, refines REQ-d-a): It MUST y.\n",
		})
		r := Diff(base, same)
		if !r.SemanticallyEmpty() || len(r.Lines()) != 0 {
			t.Fatalf("delta on identical corpora: %v", r.Lines())
		}
	})

	t.Run("added removed text kind edges", func(t *testing.T) {
		next := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**widget** (term): a gadget.\n\n" +
				"**REQ-d-a** (wire): Using the widget it MUST x.\n\n" + // kind changed
				"**REQ-d-c** (behavior): It MUST z.\n", // b removed, c added
		})
		r := Diff(base, next)
		if !slices.Equal(r.AddedRequirements, []string{"REQ-d-c"}) {
			t.Errorf("added = %v", r.AddedRequirements)
		}
		if !slices.Equal(r.RemovedRequirements, []string{"REQ-d-b"}) {
			t.Errorf("removed = %v", r.RemovedRequirements)
		}
		if !slices.Equal(r.KindChangedRequirements, []string{"REQ-d-a"}) {
			t.Errorf("kind-changed = %v", r.KindChangedRequirements)
		}
		if len(r.TextChangedRequirements) != 0 {
			t.Errorf("text-changed = %v", r.TextChangedRequirements)
		}
		if len(r.RemovedEdges) == 0 {
			t.Error("expected removed refines edge")
		}
		if r.SemanticallyEmpty() {
			t.Error("semantic delta not detected")
		}
	})

	t.Run("text and kind change together report both", func(t *testing.T) {
		next := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**widget** (term): a gadget.\n\n" +
				"**REQ-d-a** (wire): Using the widget it MUST x differently.\n\n" +
				"**REQ-d-b** (behavior, refines REQ-d-a): It MUST y.\n",
		})
		r := Diff(base, next)
		if !slices.Equal(r.TextChangedRequirements, []string{"REQ-d-a"}) ||
			!slices.Equal(r.KindChangedRequirements, []string{"REQ-d-a"}) {
			t.Fatalf("independent axes not reported: text=%v kind=%v",
				r.TextChangedRequirements, r.KindChangedRequirements)
		}
	})

	t.Run("text change", func(t *testing.T) {
		next := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**widget** (term): a gadget.\n\n" +
				"**REQ-d-a** (behavior): Using the widget it MUST x differently.\n\n" +
				"**REQ-d-b** (behavior, refines REQ-d-a): It MUST y.\n",
		})
		r := Diff(base, next)
		if !slices.Equal(r.TextChangedRequirements, []string{"REQ-d-a"}) {
			t.Fatalf("text-changed = %v", r.TextChangedRequirements)
		}
	})

	t.Run("pure reorganization is semantically empty", func(t *testing.T) {
		reorg := compileFiles(t, map[string]string{
			"specs/one.md": "# T\n\n## Deep\n\n**REQ-d-a** (behavior): Using the widget it MUST x.\n",
			"specs/two.md": "# U\n\n**widget** (term): a gadget.\n\n**REQ-d-b** (behavior, refines REQ-d-a): It MUST y.\n",
		})
		r := Diff(base, reorg)
		if !r.SemanticallyEmpty() {
			t.Fatalf("reorg produced semantic delta: %v", r.Lines())
		}
		if len(r.MetadataOnlyRequirements) == 0 {
			t.Fatal("expected metadata-only entries for moved requirements")
		}
	})

	t.Run("term rename case is metadata-only", func(t *testing.T) {
		next := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**Widget** (term): a gadget.\n\n" +
				"**REQ-d-a** (behavior): Using the widget it MUST x.\n\n" +
				"**REQ-d-b** (behavior, refines REQ-d-a): It MUST y.\n",
		})
		r := Diff(base, next)
		if len(r.AddedTerms) != 0 || len(r.RemovedTerms) != 0 {
			t.Fatalf("case rename treated as identity change: %v", r.Lines())
		}
		if !slices.Equal(r.MetadataOnlyTerms, []string{"Widget"}) {
			t.Fatalf("metadata-only terms = %v", r.MetadataOnlyTerms)
		}
		if !r.SemanticallyEmpty() {
			t.Fatalf("case-only term rename produced semantic delta: %v", r.Lines())
		}
	})
}
