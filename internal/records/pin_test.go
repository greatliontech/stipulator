package records

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/stipulate"
)

// storeWith loads a store holding one binding file with the given text.
func storeWith(t *testing.T, bindings string) *Store {
	t.Helper()
	store, err := Load(fstest.MapFS{
		".stipulator/bindings/t.textproto": {Data: []byte(bindings)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// TestPinNeverRewritesDifferingContent pins REQ-pin-backfill's core rule:
// the blanket form sets only UNSET content pins — a differing pin is the
// consent trail, rewritten only through the named editorial path, so
// staleness cannot be laundered.
//
//gofresh:pure
func TestPinNeverRewritesDifferingContent(t *testing.T) {
	stipulate.Covers(t, "REQ-pin-backfill")
	stale := strings.Repeat("0", 64)
	current := strings.Repeat("1", 64)
	store := storeWith(t, "bindings {\n  requirement_id: \"REQ-r-b\"\n  backend: \"go\"\n  symbol: \"example.com/p.G\"\n  role: BINDING_ROLE_IMPLEMENTS\n  content_hash: \""+stale+"\"\n}\n\nbindings {\n  requirement_id: \"REQ-r-a\"\n  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n  content_hash: \""+stale+"\"\n}\n\nbindings {\n  requirement_id: \"REQ-r-a\"\n  backend: \"go\"\n  symbol: \"example.com/p.F2\"\n  role: BINDING_ROLE_TESTS\n  content_hash: \""+stale+"\"\n}\n")
	ups, preserved, _, err := Pin(store, map[string]string{"REQ-r-a": current, "REQ-r-b": current}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 0 {
		t.Fatalf("differing content pins rewritten by the blanket form: %v", ups)
	}
	// Deduplicated across bindings of one requirement, sorted for a
	// deterministic response line.
	if len(preserved) != 2 || preserved[0] != "REQ-r-a" || preserved[1] != "REQ-r-b" {
		t.Fatalf("preserved differing pins = %v, want [REQ-r-a REQ-r-b]", preserved)
	}
}

// TestPinReportsRewrittenShapePins pins REQ-pin-backfill's shape-side
// reporting: a differing shape pin is rewritten AND named — the rewrite
// clears verification's shape-mismatch signal, the one trace that a
// bound implementation moved — while a backfilled unset shape pin
// cleared nothing and stays unreported.
//
//gofresh:pure
func TestPinReportsRewrittenShapePins(t *testing.T) {
	stipulate.Covers(t, "REQ-pin-backfill")
	oldShape := strings.Repeat("a", 64)
	newShape := strings.Repeat("b", 64)
	store := storeWith(t, "bindings {\n  requirement_id: \"REQ-r-a\"\n  backend: \"go\"\n  symbol: \"example.com/p.Moved\"\n  role: BINDING_ROLE_IMPLEMENTS\n  shape_hash: \""+oldShape+"\"\n}\n\nbindings {\n  requirement_id: \"REQ-r-b\"\n  backend: \"go\"\n  symbol: \"example.com/p.Fresh\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n")
	ups, _, reshaped, err := Pin(store, nil, map[string]string{
		ShapeKey("go", "example.com/p.Moved"): newShape,
		ShapeKey("go", "example.com/p.Fresh"): newShape,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 {
		t.Fatalf("expected the one binding file rewritten, got %d", len(ups))
	}
	if len(reshaped) != 1 || reshaped[0] != "example.com/p.Moved" {
		t.Fatalf("reshaped = %v, want the differing symbol alone (an unset backfill cleared no signal)", reshaped)
	}
}

// TestShapeMismatchedReportsOnlyNamedDiffering pins the ids-form
// warning's derivation (REQ-pin-backfill): only the named requirements'
// bindings are judged, and only a set-and-differing shape pin counts —
// unset pins and other requirements' mismatches are not this answer's
// business.
//
//gofresh:pure
func TestShapeMismatchedReportsOnlyNamedDiffering(t *testing.T) {
	stipulate.Covers(t, "REQ-pin-backfill")
	oldShape := strings.Repeat("a", 64)
	newShape := strings.Repeat("b", 64)
	store := storeWith(t, "bindings {\n  requirement_id: \"REQ-r-a\"\n  backend: \"go\"\n  symbol: \"example.com/p.Moved\"\n  role: BINDING_ROLE_IMPLEMENTS\n  shape_hash: \""+oldShape+"\"\n}\n\nbindings {\n  requirement_id: \"REQ-r-a\"\n  backend: \"go\"\n  symbol: \"example.com/p.Unset\"\n  role: BINDING_ROLE_TESTS\n}\n\nbindings {\n  requirement_id: \"REQ-r-b\"\n  backend: \"go\"\n  symbol: \"example.com/p.OtherMoved\"\n  role: BINDING_ROLE_IMPLEMENTS\n  shape_hash: \""+oldShape+"\"\n}\n")
	shapes := map[string]string{
		ShapeKey("go", "example.com/p.Moved"):      newShape,
		ShapeKey("go", "example.com/p.Unset"):      newShape,
		ShapeKey("go", "example.com/p.OtherMoved"): newShape,
	}
	got := ShapeMismatched(store, []string{"REQ-r-a"}, shapes)
	if len(got) != 1 || len(got["REQ-r-a"]) != 1 || got["REQ-r-a"][0] != "example.com/p.Moved" {
		t.Fatalf("ShapeMismatched = %v, want REQ-r-a -> [example.com/p.Moved]", got)
	}
}
