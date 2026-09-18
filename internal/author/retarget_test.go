package author

import (
	"slices"
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

	res, err := Retarget(fsys, resolver, "go", "example.com/old", "example.com/new")
	if err != nil {
		t.Fatal(err)
	}
	ups, rows := res.Updates, res.Rows
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
	if _, err := Retarget(fsys, map[string]verify.Backend{"go": fakeBackend{
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
	if _, err := Retarget(collideFS, resolver, "go", "example.com/old", "example.com/new"); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("collision accepted: %v", err)
	}

	// Nothing matched is an error, never a silent no-op.
	if _, err := Retarget(fsys, resolver, "go", "example.com/ghost", "example.com/new"); err == nil {
		t.Fatal("prefix matching nothing accepted")
	}
	if _, err := Retarget(fsys, resolver, "go", "example.com/old", "example.com/old"); err == nil {
		t.Fatal("identical prefixes accepted")
	}
	if _, err := Retarget(fsys, map[string]verify.Backend{}, "go", "example.com/old", "example.com/new"); err == nil {
		t.Fatal("missing backend accepted")
	}
}

// A full-symbol --from is the degenerate member boundary: the
// single-symbol rename repair rewrites exactly that binding and no
// sibling sharing the prefix (REQ-change-retarget).
//
//gofresh:pure
func TestRetargetFullSymbolIsDegenerateBoundary(t *testing.T) {
	stipulate.Covers(t, "REQ-change-retarget")
	fsys := testFS(map[string]string{
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-a\" backend: \"go\" symbol: \"example.com/pkg.oldName\" role: BINDING_ROLE_IMPLEMENTS }\n" +
			"bindings { requirement_id: \"REQ-au-a\" backend: \"go\" symbol: \"example.com/pkg.oldNameKeeps\" role: BINDING_ROLE_TESTS }\n",
	})
	resolver := map[string]verify.Backend{"go": fakeBackend{
		"example.com/pkg.newName": strings.Repeat("n", 64),
	}}
	res, err := Retarget(fsys, resolver, "go", "example.com/pkg.oldName", "example.com/pkg.newName")
	if err != nil {
		t.Fatal(err)
	}
	rows := res.Rows
	if len(rows) != 1 || rows[0].Old != "example.com/pkg.oldName" || rows[0].New != "example.com/pkg.newName" {
		t.Fatalf("rows = %+v, want exactly the full-symbol rename", rows)
	}
}

// clauseDoc declares a requirement with two labelled clauses beside the
// shared fixture, so a claim's resolved clause is part of its identity.
const clauseDoc = "# C\n\n**REQ-au-c** (behavior): It MUST support both operations:\n\n" +
	"- **prepared**: Prepare operations.\n" +
	"- **granted**: Bound the grant.\n"

// A retarget's collision is two claims of one identity — requirement,
// backend, symbol, role, and RESOLVED clause: two claims on distinct
// clauses of one symbol are two claims and never collide, across
// binding files included, while a rewrite collapsing two claims onto
// one clause — spelled alike, or by label beside ordinal — is refused,
// and a pre-existing duplicate outside the selection is refused as
// verification's hygiene refuses it (REQ-change-retarget,
// REQ-evidence-clause-claim).
//
//gofresh:pure
func TestRetargetJudgesCollisionsByClaimIdentity(t *testing.T) {
	stipulate.Covers(t, "REQ-change-retarget", "REQ-evidence-clause-claim")
	resolver := map[string]verify.Backend{"go": fakeBackend{
		"example.com/new.Claim": strings.Repeat("c", 64),
		"example.com/new.New":   strings.Repeat("n", 64),
	}}
	// An unrelated move over a store holding two distinct-clause claims
	// on one untouched symbol: one rewrite, no collision.
	distinct := testFS(map[string]string{
		"specs/c.md": clauseDoc,
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/keep.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared\" }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/keep.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"granted\" }\n" +
			// An unscoped claim beside the scoped ones, and the same
			// clause under another role: each its own identity.
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/keep.Claim\" role: BINDING_ROLE_IMPLEMENTS }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/keep.Claim\" role: BINDING_ROLE_TESTS clause_label: \"granted\" }\n" +
			"bindings { requirement_id: \"REQ-au-b\" backend: \"go\" symbol: \"example.com/old.New\" role: BINDING_ROLE_IMPLEMENTS }\n",
	})
	res, err := Retarget(distinct, resolver, "go", "example.com/old", "example.com/new")
	if err != nil || len(res.Rows) != 1 || res.Rows[0].New != "example.com/new.New" {
		t.Fatalf("distinct clause claims on an untouched symbol refused the move: %+v, %v", res, err)
	}
	// The shared symbol itself moves, its two claims split across
	// files: both rewritten, still two claims.
	shared := testFS(map[string]string{
		"specs/c.md": clauseDoc,
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/old.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared\" }\n",
		".stipulator/bindings/n.textproto": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/old.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_ordinal: 2 }\n",
	})
	res, err = Retarget(shared, resolver, "go", "example.com/old", "example.com/new")
	if err != nil || len(res.Rows) != 2 || len(res.Updates) != 2 {
		t.Fatalf("a shared symbol's distinct clause claims: %+v, %v", res, err)
	}
	// A rewrite collapsing two claims onto one resolved clause is
	// refused: spelled alike, and by label beside ordinal — the corpus
	// resolves the alias.
	for name, store := range map[string]string{
		"alike": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/old.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared\" }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/new.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared\" }\n",
		"alias": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/old.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared\" }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/new.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_ordinal: 1 }\n",
	} {
		fsys := testFS(map[string]string{"specs/c.md": clauseDoc, ".stipulator/bindings/m.textproto": store})
		_, err := Retarget(fsys, resolver, "go", "example.com/old", "example.com/new")
		if err == nil || !strings.Contains(err.Error(), "collides") || !strings.Contains(err.Error(), "prepared") {
			t.Fatalf("%s: collapsing two claims onto one clause accepted: %v", name, err)
		}
	}
	// Every pair of a group is judged, not the last: a label beside a
	// different ordinal and then the label's own ordinal collides on
	// the first member.
	trio := testFS(map[string]string{
		"specs/c.md": clauseDoc,
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/old.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared\" }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/new.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_ordinal: 2 }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/new.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_ordinal: 1 }\n",
	})
	if _, err := Retarget(trio, resolver, "go", "example.com/old", "example.com/new"); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("a three-member group's first-vs-last alias accepted: %v", err)
	}
	// A label beside a DIFFERENT ordinal resolves to two clauses: no
	// collision, the corpus consulted and answering distinct.
	twoClauses := testFS(map[string]string{
		"specs/c.md": clauseDoc,
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/old.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared\" }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/new.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_ordinal: 2 }\n",
	})
	if _, err := Retarget(twoClauses, resolver, "go", "example.com/old", "example.com/new"); err != nil {
		t.Fatalf("a label beside a different ordinal refused: %v", err)
	}
	// A pre-existing duplicate outside the selection is refused: the
	// whole store is judged, as verification's hygiene judges it.
	duplicate := testFS(map[string]string{
		"specs/c.md": clauseDoc,
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/keep.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared\" }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/keep.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_ordinal: 1 }\n" +
			"bindings { requirement_id: \"REQ-au-b\" backend: \"go\" symbol: \"example.com/old.New\" role: BINDING_ROLE_IMPLEMENTS }\n",
	})
	if _, err := Retarget(duplicate, resolver, "go", "example.com/old", "example.com/new"); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("a pre-existing alias duplicate outside the selection accepted: %v", err)
	}
	// The corpus is consulted only for an alias pair: a store without
	// one retargets over a corpus that does not compile.
	broken := testFS(map[string]string{
		"specs/c.md": "# broken\n\n**REQ-au-c** (behavior): no keyword here.\n",
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/keep.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared\" }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/keep.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"granted\" }\n" +
			"bindings { requirement_id: \"REQ-au-b\" backend: \"go\" symbol: \"example.com/old.New\" role: BINDING_ROLE_IMPLEMENTS }\n",
	})
	if _, err := Retarget(broken, resolver, "go", "example.com/old", "example.com/new"); err != nil {
		t.Fatalf("a prefix move without an alias pair read the corpus: %v", err)
	}
	// An alias pair over that corpus needs it: the operation refuses,
	// naming the requirement whose clauses it could not resolve.
	brokenAlias := testFS(map[string]string{
		"specs/c.md": "# broken\n\n**REQ-au-c** (behavior): no keyword here.\n",
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/old.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared\" }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/new.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_ordinal: 1 }\n",
	})
	if _, err := Retarget(brokenAlias, resolver, "go", "example.com/old", "example.com/new"); err == nil || !strings.Contains(err.Error(), "REQ-au-c") || !strings.Contains(err.Error(), "does not compile") {
		t.Fatalf("an alias pair over a broken corpus: %v", err)
	}
	// One corpus per operation: an alias pair that made the collision
	// check compile it beside a moved member name whose enforcement
	// pointer the pointer half rewrites — the reused spec places the
	// rewrite and the re-pin.
	pointed := testFS(map[string]string{
		"specs/c.md": "# C\n\n**REQ-au-c** (behavior): It MUST support both operations. Enforced by `Claim`.\n\n- **prepared**: Prepare operations.\n- **granted**: Bound the grant.\n",
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/old.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared\" }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/old.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_ordinal: 2 }\n",
	})
	renamed := map[string]verify.Backend{"go": fakeBackend{"example.com/new.Renamed": strings.Repeat("r", 64)}}
	res, err = Retarget(pointed, renamed, "go", "example.com/old.Claim", "example.com/new.Renamed")
	if err != nil || len(res.Rows) != 2 || len(res.Pointers) != 1 || res.Pointers[0] != (PointerRow{Requirement: "REQ-au-c", Document: "specs/c.md", Old: "Claim", New: "Renamed"}) {
		t.Fatalf("the pointer half over the collision check's corpus: %+v, %v", res, err)
	}
	if !slices.ContainsFunc(res.Updates, func(u Update) bool {
		return u.Path == "specs/c.md" && strings.Contains(string(u.Content), "Enforced by `Renamed`.")
	}) {
		t.Fatalf("the pointer rewrite did not land: %+v", res.Updates)
	}
	// A hand-written empty label is a spelling of its own, never the
	// unscoped claim: beside an unscoped claim it collides with nothing
	// here, and names no clause for hygiene (REQ-evidence-clause-claim).
	emptyLabel := testFS(map[string]string{
		"specs/c.md": clauseDoc,
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/old.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"\" }\n" +
			"bindings { requirement_id: \"REQ-au-c\" backend: \"go\" symbol: \"example.com/new.Claim\" role: BINDING_ROLE_IMPLEMENTS }\n",
	})
	if _, err := Retarget(emptyLabel, resolver, "go", "example.com/old", "example.com/new"); err != nil {
		t.Fatalf("an empty label read as the unscoped claim: %v", err)
	}
}
