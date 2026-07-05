package author

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/prototext"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// disposeFS builds a corpus whose records were authored against oldDoc,
// then swaps the spec to newDoc — the mid-disposition state.
func disposeFS(t *testing.T, oldDoc, newDoc string, extra map[string]string) fstest.MapFS {
	t.Helper()
	fsys := testFS(nil)
	fsys["specs/a.md"] = &fstest.MapFile{Data: []byte(oldDoc)}
	// Author a pinned binding against the old text through the real verb.
	up, err := Bind(fsys, backends, bindReq("REQ-au-a", "example.com/p.F"))
	if err != nil {
		t.Fatal(err)
	}
	fsys[up.Path] = &fstest.MapFile{Data: up.Content}
	fsys["specs/a.md"] = &fstest.MapFile{Data: []byte(newDoc)}
	for p, c := range extra {
		fsys[p] = &fstest.MapFile{Data: []byte(c)}
	}
	return fsys
}

func TestEditorial(t *testing.T) {
	stipulate.Covers(t, "REQ-change-editorial")
	oldDoc := "# T\n\n**REQ-au-a** (behavior): It MUST x.\n\n**REQ-au-b** (behavior): It MUST y.\n"
	newDoc := "# T\n\n**REQ-au-a** (behavior): It MUST x, reworded.\n\n**REQ-au-b** (behavior): It MUST y.\n"
	fsys := disposeFS(t, oldDoc, newDoc, nil)

	// A second stale binding file for the same requirement: re-pin spans
	// files and the update list is canonically ordered.
	fsys[".stipulator/bindings/zz-extra.textproto"] = &fstest.MapFile{Data: []byte(
		"bindings {\n  requirement_id: \"REQ-au-a\"\n  content_hash: \"" + strings.Repeat("0", 64) + "\"\n  backend: \"go\"\n  symbol: \"example.com/p.G\"\n  role: BINDING_ROLE_TESTS\n}\n")}
	ups, err := Editorial(fsys, "REQ-au-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 2 {
		t.Fatalf("updates = %d", len(ups))
	}
	if !(ups[0].Path < ups[1].Path) {
		t.Fatal("updates not canonically ordered")
	}
	set := &stipulatorv1.BindingSet{}
	if err := prototext.Unmarshal(ups[0].Content, set); err != nil {
		t.Fatal(err)
	}
	// Re-pinned to the corpus's current hash, no other change.
	spec, err := compileClean(fsys)
	if err != nil {
		t.Fatal(err)
	}
	current := ""
	for _, r := range spec.GetRequirements() {
		if r.GetId() == "REQ-au-a" {
			current = r.GetContentHash()
		}
	}
	if set.GetBindings()[0].GetContentHash() != current {
		t.Fatal("editorial did not re-pin to the current hash")
	}

	if _, err := Editorial(fsys, "REQ-au-b"); err == nil {
		t.Fatal("editorial with nothing stale succeeded")
	}
	if _, err := Editorial(fsys, "REQ-au-ghost"); err == nil {
		t.Fatal("unknown requirement accepted")
	}

	// Error arms propagate, never swallow — hardening found both
	// unwitnessed: a corpus that no longer compiles, and a broken store.
	fsys["specs/broken.md"] = &fstest.MapFile{Data: []byte("# B\n\n**REQ-au-a** (behavior): Redeclared, it MUST clash.\n")}
	if _, err := Editorial(fsys, "REQ-au-a"); err == nil {
		t.Fatal("non-compiling corpus swallowed")
	}
	delete(fsys, "specs/broken.md")
	fsys[".stipulator/bindings/broken.textproto"] = &fstest.MapFile{Data: []byte("not textproto {{{")}
	if _, err := Editorial(fsys, "REQ-au-a"); err == nil {
		t.Fatal("broken store swallowed")
	}
}

func TestRetire(t *testing.T) {
	stipulate.Covers(t, "REQ-change-retire")
	oldDoc := "# T\n\n**REQ-au-a** (behavior): It MUST x.\n\n**REQ-au-b** (behavior): It MUST y.\n"
	newDoc := "# T\n\n**REQ-au-b** (behavior): It MUST y.\n" // a removed
	fsys := disposeFS(t, oldDoc, newDoc, map[string]string{
		".stipulator/gaps/au-a.textproto": "requirement_id: \"REQ-au-a\"\nreason: \"r\"\nlands { attested { condition: \"x\" } }\n",
	})

	ups, err := Retire(fsys, "REQ-au-a", false)
	if err != nil {
		t.Fatal(err)
	}
	var tombstones []byte
	deletedGap, deletedBindings := false, false
	for _, up := range ups {
		switch {
		case up.Path == records.TombstonesPath:
			tombstones = up.Content
		case up.Path == ".stipulator/gaps/au-a.textproto" && up.Content == nil:
			deletedGap = true
		case strings.HasPrefix(up.Path, ".stipulator/bindings/") && up.Content == nil:
			deletedBindings = true
		}
	}
	if !strings.Contains(string(tombstones), `retired: "REQ-au-a"`) {
		t.Fatalf("tombstone missing:\n%s", tombstones)
	}
	if !deletedGap || !deletedBindings {
		t.Fatalf("gap deleted=%v bindings deleted=%v", deletedGap, deletedBindings)
	}

	t.Run("still declared refuses", func(t *testing.T) {
		fsys2 := disposeFS(t, oldDoc, oldDoc, nil)
		if _, err := Retire(fsys2, "REQ-au-a", false); err == nil || !strings.Contains(err.Error(), "still declared") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("dangling reference refuses", func(t *testing.T) {
		ref := "# T\n\n**REQ-au-b** (behavior): Like REQ-au-a it MUST y.\n"
		fsys2 := disposeFS(t, oldDoc, ref, nil)
		if _, err := Retire(fsys2, "REQ-au-a", false); err == nil || !strings.Contains(err.Error(), "does not compile") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestSupersede(t *testing.T) {
	stipulate.Covers(t, "REQ-change-split-merge", "REQ-change-transient")
	oldDoc := "# T\n\n**REQ-au-a** (behavior): It MUST x.\n\n**REQ-au-b** (behavior): It MUST y.\n"
	split := "# T\n\n**REQ-au-b** (behavior): It MUST y.\n\n" +
		"**REQ-au-a1** (behavior, supersedes REQ-au-a): First half, it MUST x1.\n\n" +
		"**REQ-au-a2** (behavior, supersedes REQ-au-a): Second half, it MUST x2.\n"
	fsys := disposeFS(t, oldDoc, split, nil)

	ups, err := Supersede(fsys, []string{"REQ-au-a"}, []string{"REQ-au-a1", "REQ-au-a2"}, false)
	if err != nil {
		t.Fatal(err)
	}
	// The only persistent effects: tombstones + record rewrites. Collect
	// retargeted bindings and verify stale-by-contract (no content pin).
	retargeted := map[string]bool{}
	for _, up := range ups {
		if up.Content == nil || !strings.HasPrefix(up.Path, ".stipulator/bindings/") {
			continue
		}
		set := &stipulatorv1.BindingSet{}
		if err := prototext.Unmarshal(up.Content, set); err != nil {
			t.Fatal(err)
		}
		for _, b := range set.GetBindings() {
			retargeted[b.GetRequirementId()] = true
			if b.GetContentHash() != "" {
				t.Fatalf("retargeted binding born pinned (must be stale): %v", b)
			}
			if b.GetSymbol() != "example.com/p.F" {
				t.Fatalf("symbol lost in retarget: %v", b)
			}
		}
	}
	if !retargeted["REQ-au-a1"] || !retargeted["REQ-au-a2"] {
		t.Fatalf("bindings not retargeted to both successors: %v", retargeted)
	}
	for _, up := range ups {
		if !strings.HasPrefix(up.Path, ".stipulator/") {
			t.Fatalf("disposition wrote outside the record stores: %s", up.Path)
		}
	}

	t.Run("missing supersedes clause refuses", func(t *testing.T) {
		noEdge := "# T\n\n**REQ-au-b** (behavior): It MUST y.\n\n" +
			"**REQ-au-a1** (behavior): It MUST x1.\n"
		fsys2 := disposeFS(t, oldDoc, noEdge, nil)
		_, err := Supersede(fsys2, []string{"REQ-au-a"}, []string{"REQ-au-a1"}, false)
		if err == nil || !strings.Contains(err.Error(), "does not declare") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown successor refuses", func(t *testing.T) {
		fsys2 := disposeFS(t, oldDoc, split, nil)
		if _, err := Supersede(fsys2, []string{"REQ-au-a"}, []string{"REQ-au-ghost"}, false); err == nil {
			t.Fatal("unknown successor accepted")
		}
	})
}
