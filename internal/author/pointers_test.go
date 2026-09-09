package author

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// A retarget that moves a bound symbol's member name rewrites every
// enforcement pointer naming the old member in that binding's
// requirement, in the document, re-pins the requirement's content to
// the rewritten text by the same operation, reports the pointer rows
// beside the binding rows, and leaves a prefix move that keeps the
// member alone; a pointer whose requirement's source cannot be placed
// once in its document refuses the whole retarget
// (REQ-change-enforcement-pointers).
//
//gofresh:pure
func TestRetargetRewritesEnforcementPointersAndRepins(t *testing.T) {
	stipulate.Covers(t, "REQ-change-enforcement-pointers", "REQ-change-retarget")
	spec := "# T\n\n**REQ-au-a** (behavior): The `TestX` fast path MUST x, which is Enforced by `TestX` in prose. Enforced by `pkg.Q`, `TestX` and `TestKept`.\n\n- **alpha** first\n\n**REQ-au-b** (behavior): It MUST y. Enforced by `TestX`.\n"
	fsys := testFS(map[string]string{
		"specs/a.md": spec,
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-au-a\" content_hash: \"c1\" backend: \"go\" symbol: \"example.com/old.TestX\" role: BINDING_ROLE_TESTS }\n" +
			"bindings { requirement_id: \"REQ-au-a\" content_hash: \"c1\" backend: \"go\" symbol: \"example.com/old.TestX\" role: BINDING_ROLE_IMPLEMENTS }\n" +
			"bindings { requirement_id: \"REQ-au-a\" content_hash: \"c1\" backend: \"go\" symbol: \"example.com/old.TestKept\" role: BINDING_ROLE_TESTS clause_label: \"alpha\" }\n" +
			"bindings { requirement_id: \"REQ-au-b\" backend: \"go\" symbol: \"example.com/other.TestX\" role: BINDING_ROLE_TESTS }\n",
		".stipulator/attestations/m.textproto": "attestations { requirement_id: \"REQ-au-a\" content_hash: \"c1\" reason: \"reviewed\" }\n",
	})
	resolver := map[string]verify.Backend{"go": fakeBackend{"example.com/old.TestY": strings.Repeat("y", 64)}}
	res, err := Retarget(fsys, resolver, "go", "example.com/old.TestX", "example.com/old.TestY")
	if err != nil {
		t.Fatal(err)
	}
	// Two bindings of one requirement on one symbol (two roles) are two
	// rows and ONE pointer rewrite.
	if len(res.Rows) != 2 || res.Rows[0].New != "example.com/old.TestY" || res.Rows[1].New != "example.com/old.TestY" {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if len(res.Pointers) != 1 || res.Pointers[0] != (PointerRow{Requirement: "REQ-au-a", Document: "specs/a.md", Old: "TestX", New: "TestY"}) {
		t.Fatalf("pointer rows = %+v", res.Pointers)
	}
	var doc, bindings, attestations *Update
	for i := range res.Updates {
		switch res.Updates[i].Path {
		case "specs/a.md":
			doc = &res.Updates[i]
		case ".stipulator/bindings/m.textproto":
			bindings = &res.Updates[i]
		case ".stipulator/attestations/m.textproto":
			attestations = &res.Updates[i]
		}
	}
	if doc == nil || bindings == nil || attestations == nil {
		t.Fatalf("updates = %+v", res.Updates)
	}
	if !doc.Document || bindings.Document || attestations.Document {
		t.Fatal("the document update alone carries the document mark")
	}
	// Only the pointer sentence's span is rewritten: the prose code
	// span with the same spelling stays.
	want := strings.Replace(spec, "Enforced by `pkg.Q`, `TestX` and `TestKept`.", "Enforced by `pkg.Q`, `TestY` and `TestKept`.", 1)
	if string(doc.Content) != want || string(doc.Prior) != spec {
		t.Fatalf("document rewrite = %q (prior %q)", doc.Content, doc.Prior)
	}
	// REQ-au-b's pointer names another binding's TestX: its text is
	// untouched — the rewrite is the binding's requirement's alone.
	if !strings.Contains(string(doc.Content), "It MUST y. Enforced by `TestX`.") {
		t.Fatalf("a pointer of another requirement moved: %q", doc.Content)
	}
	// The content pin follows the rewritten text: the hash the
	// rewritten corpus compiles to, on every binding of the requirement.
	rewritten, diags, err := compile.Compile(fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: doc.Content},
	})
	if err != nil || len(diags) > 0 {
		t.Fatalf("recompile: %v %v", err, diags)
	}
	hash := records.ByID(rewritten)["REQ-au-a"].GetContentHash()
	content := string(bindings.Content)
	if strings.Count(content, "content_hash: \""+hash+"\"") != 3 || strings.Contains(content, "content_hash: \"c1\"") {
		t.Fatalf("content pins not re-derived to %s:\n%s", hash, content)
	}
	// The attestation's pin follows too: the tool's own edit moved no
	// judgment, and a stale pin would read the requirement red.
	if a := string(attestations.Content); !strings.Contains(a, "content_hash: \""+hash+"\"") || strings.Contains(a, "\"c1\"") || attestations.Prior == nil {
		t.Fatalf("attestation pin not re-derived:\n%s", a)
	}
	if !strings.Contains(content, "example.com/old.TestY") || !strings.Contains(content, "example.com/other.TestX") {
		t.Fatalf("binding rewrite:\n%s", content)
	}
	if string(bindings.Prior) == "" || bindings.PriorAbsent {
		t.Fatal("the binding update lost its compare-and-swap prior")
	}
	// The re-pin's consent lines ride the result: the editorial names
	// the clause each clause claim now denotes, the attestation's
	// re-pin is named.
	if joined := strings.Join(res.Consented, "\n"); !strings.Contains(joined, "example.com/old.TestKept now claims clause 1 `alpha`") || !strings.Contains(joined, "attestation of REQ-au-a") {
		t.Fatalf("consent lines = %q", joined)
	}
	// A prefix move keeping the member name moves no pointer.
	res, err = Retarget(fsys, map[string]verify.Backend{"go": fakeBackend{"example.com/new.TestX": strings.Repeat("n", 64), "example.com/new.TestKept": strings.Repeat("k", 64)}}, "go", "example.com/old", "example.com/new")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pointers) != 0 || len(res.Updates) != 1 {
		t.Fatalf("a member-preserving move touched pointers: %+v %+v", res.Pointers, res.Updates)
	}
	// A member-preserving move needs no compiling corpus: retarget stays
	// a repair verb over a broken one.
	broken := testFS(map[string]string{
		"specs/a.md":                       "# T\n\n**REQ-au-a** (behavior): It MUST x.\n\n**REQ-au-a** (behavior): twice.\n",
		".stipulator/bindings/m.textproto": "bindings { requirement_id: \"REQ-au-a\" backend: \"go\" symbol: \"example.com/old.TestX\" role: BINDING_ROLE_TESTS }\n",
	})
	if _, err := Retarget(broken, map[string]verify.Backend{"go": fakeBackend{"example.com/new.TestX": strings.Repeat("n", 64)}}, "go", "example.com/old", "example.com/new"); err != nil {
		t.Fatalf("a prefix move over a broken corpus: %v; want the repair to proceed", err)
	}
	// An unplaceable rewrite refuses the whole retarget: the requirement's
	// source also appears verbatim in a fenced block.
	twice := spec + "\n```\n" + strings.Join(strings.Split(spec, "\n\n")[1:3], "\n\n") + "\n```\n"
	fsys["specs/a.md"] = &fstest.MapFile{Data: []byte(twice)}
	if _, err := Retarget(fsys, resolver, "go", "example.com/old.TestX", "example.com/old.TestY"); err == nil || !strings.Contains(err.Error(), "occurs 2 times") {
		t.Fatalf("an unplaceable rewrite: %v; want a refusal", err)
	}
}
