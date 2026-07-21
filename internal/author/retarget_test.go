package author

import (
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// A symbol retarget rewrites every boundary-matched binding of the
// named backend all-or-nothing, re-derives shape pins from the resolved
// replacements, leaves content pins and everything else untouched, and
// reports each old-to-new identity (REQ-change-retarget).
//
//gofresh:pure
func TestRetargetSymbolsRewritesAtBoundaryAllOrNothing(t *testing.T) {
	stipulate.Covers(t, "REQ-change-retarget")
	fsys := testFS(map[string]string{
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-a\" content_hash: \"c1\" backend: \"go\" symbol: \"example.com/old/pkg.F\" role: BINDING_ROLE_IMPLEMENTS shape_hash: \"h1\" }\n" +
			"bindings { requirement_id: \"REQ-au-a\" backend: \"go\" symbol: \"example.com/old.TestX\" role: BINDING_ROLE_TESTS }\n" +
			"bindings { requirement_id: \"REQ-au-b\" backend: \"go\" symbol: \"example.com/older.TestY\" role: BINDING_ROLE_TESTS shape_hash: \"h3\" }\n" +
			"bindings { requirement_id: \"REQ-au-b\" backend: \"proto\" symbol: \"example.com/old/wire.Msg\" role: BINDING_ROLE_IMPLEMENTS }\n",
	})
	resolver := map[string]verify.Backend{"go": fakeBackend{
		"example.com/new/pkg.F": strings.Repeat("n", 64),
		"example.com/new.TestX": strings.Repeat("t", 64),
	}}

	ups, rows, err := RetargetSymbols(fsys, resolver, "go", "example.com/old", "example.com/new")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 ||
		rows[0].Old != "example.com/old.TestX" || rows[0].New != "example.com/new.TestX" ||
		rows[1].Old != "example.com/old/pkg.F" || rows[1].New != "example.com/new/pkg.F" {
		t.Fatalf("rows = %+v", rows)
	}
	if len(ups) != 1 {
		t.Fatalf("updates = %d, want the one touched file", len(ups))
	}
	content := string(ups[0].Content)
	for _, want := range []string{
		"example.com/new/pkg.F",
		"example.com/new.TestX",
		// The sibling sharing characters and the foreign backend ride
		// unchanged: the boundary and the backend filter both hold.
		"example.com/older.TestY",
		"example.com/old/wire.Msg",
		// Content pin unchanged; shape re-derived from the resolution.
		"content_hash: \"c1\"",
		strings.Repeat("n", 64),
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rewritten file misses %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "shape_hash: \"h1\"") {
		t.Fatalf("stale shape pin survived the rewrite:\n%s", content)
	}
	// The unpinned tests-role binding stays unpinned: backfilling a pin
	// is the pin verb's consent action, never a retarget side effect.
	if strings.Contains(content, strings.Repeat("t", 64)) {
		t.Fatalf("retarget backfilled a shape pin nobody authored:\n%s", content)
	}

	// All-or-nothing: an unresolvable replacement refuses the batch.
	if _, _, err := RetargetSymbols(fsys, map[string]verify.Backend{"go": fakeBackend{
		"example.com/new/pkg.F": "x",
	}}, "go", "example.com/old", "example.com/new"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unresolvable replacement accepted: %v", err)
	}

	// A collision with an existing post-rewrite identity refuses.
	collideFS := testFS(map[string]string{
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-a\" backend: \"go\" symbol: \"example.com/old.TestX\" role: BINDING_ROLE_TESTS }\n" +
			"bindings { requirement_id: \"REQ-au-a\" backend: \"go\" symbol: \"example.com/new.TestX\" role: BINDING_ROLE_TESTS }\n",
	})
	if _, _, err := RetargetSymbols(collideFS, resolver, "go", "example.com/old", "example.com/new"); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("collision accepted: %v", err)
	}

	// Nothing matched is an error, never a silent no-op.
	if _, _, err := RetargetSymbols(fsys, resolver, "go", "example.com/ghost", "example.com/new"); err == nil {
		t.Fatal("prefix matching nothing accepted")
	}
	if _, _, err := RetargetSymbols(fsys, resolver, "go", "example.com/old", "example.com/old"); err == nil {
		t.Fatal("identical prefixes accepted")
	}
	if _, _, err := RetargetSymbols(fsys, map[string]verify.Backend{}, "go", "example.com/old", "example.com/new"); err == nil {
		t.Fatal("missing backend accepted")
	}
}
