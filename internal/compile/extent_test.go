package compile

import (
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// The field-proven escape (tugboat, 2026-08-23): a vocabulary table
// following a requirement's context prose was outside the content hash,
// so editing the wire vocabulary demanded no re-consent. The context
// extent now rides the hash: editing any note or annotation block
// between the requirement and the next boundary moves the hash while
// the carried text stays the lead+payload alone
// (REQ-model-content-hash, REQ-profile-context-extent).
func TestContextExtentRidesContentHash(t *testing.T) {
	stipulate.Covers(t, "REQ-model-content-hash", "REQ-profile-context-extent")
	base := map[string]string{
		"specs/a.md": "# Doc\n\n**REQ-a** (wire): The encoding MUST use the verdict codes below.\n\nContext prose about the codes.\n\n| code | meaning |\n|------|---------|\n| 23   | stale   |\n\n**REQ-b** (behavior): The reader MUST parse.\n",
	}
	spec, diags := compileFiles(t, base)
	wantClean(t, diags)
	origA, origB := req(t, spec, "REQ-a"), req(t, spec, "REQ-b")

	// Editing the vocabulary table moves REQ-a's hash, not its text and
	// not REQ-b.
	edited := map[string]string{
		"specs/a.md": "# Doc\n\n**REQ-a** (wire): The encoding MUST use the verdict codes below.\n\nContext prose about the codes.\n\n| code | meaning |\n|------|---------|\n| 23   | stale   |\n| 24   | grown   |\n\n**REQ-b** (behavior): The reader MUST parse.\n",
	}
	spec2, diags := compileFiles(t, edited)
	wantClean(t, diags)
	editedA, editedB := req(t, spec2, "REQ-a"), req(t, spec2, "REQ-b")
	if editedA.GetContentHash() == origA.GetContentHash() {
		t.Fatal("editing the context table did not move REQ-a's content hash — the re-consent escape")
	}
	if editedA.GetText() != origA.GetText() {
		t.Fatalf("carried text moved with the extent: %q vs %q", editedA.GetText(), origA.GetText())
	}
	if editedB.GetContentHash() != origB.GetContentHash() {
		t.Fatal("REQ-b's hash moved on an edit outside its extent")
	}

	// Editing the context prose moves the hash too.
	prose := map[string]string{
		"specs/a.md": "# Doc\n\n**REQ-a** (wire): The encoding MUST use the verdict codes below.\n\nDifferent context prose about the codes.\n\n| code | meaning |\n|------|---------|\n| 23   | stale   |\n\n**REQ-b** (behavior): The reader MUST parse.\n",
	}
	spec3, diags := compileFiles(t, prose)
	wantClean(t, diags)
	if req(t, spec3, "REQ-a").GetContentHash() == origA.GetContentHash() {
		t.Fatal("editing the context prose did not move REQ-a's content hash")
	}
}

// Block boundaries ride the preimage: merging a context paragraph into
// the requirement's lead yields the same word sequence but a DIFFERENT
// hash — the merge changes the words' normative status (they enter
// keyword detection and reference attribution), so it must demand
// re-consent even though the concatenated words are unchanged
// (REQ-model-content-hash's boundary-preservation clause).
func TestExtentBoundaryRidesPreimage(t *testing.T) {
	stipulate.Covers(t, "REQ-model-content-hash")
	separate := map[string]string{
		"specs/a.md": "# Doc\n\n**REQ-a** (behavior): A MUST hold.\n\nExtra context words.\n",
	}
	merged := map[string]string{
		"specs/a.md": "# Doc\n\n**REQ-a** (behavior): A MUST hold. Extra context words.\n",
	}
	specSep, diags := compileFiles(t, separate)
	wantClean(t, diags)
	specMer, diags := compileFiles(t, merged)
	wantClean(t, diags)
	sep, mer := req(t, specSep, "REQ-a"), req(t, specMer, "REQ-a")
	if sep.GetContentHash() == mer.GetContentHash() {
		t.Fatal("merging extent words into the lead did not move the hash — the boundary collapsed out of the preimage")
	}
	if sep.GetText() == mer.GetText() {
		t.Fatal("test is vacuous: the two layouts carry identical text")
	}
}

// Extent boundaries: the next identity lead, a heading, and a thematic
// break each close the extent — content beyond them never moves the
// hash (REQ-profile-context-extent).
func TestContextExtentBoundaries(t *testing.T) {
	stipulate.Covers(t, "REQ-profile-context-extent")
	base := map[string]string{
		"specs/a.md": "# Doc\n\n**REQ-a** (behavior): A MUST hold.\n\n## Section\n\nSection intro prose.\n\n**REQ-b** (behavior): B MUST hold.\n\n---\n\nFree-standing context after the break.\n",
	}
	spec, diags := compileFiles(t, base)
	wantClean(t, diags)
	origA, origB := req(t, spec, "REQ-a"), req(t, spec, "REQ-b")

	// Past a heading: the section intro is not REQ-a's extent.
	afterHeading := map[string]string{
		"specs/a.md": "# Doc\n\n**REQ-a** (behavior): A MUST hold.\n\n## Section\n\nEDITED section intro prose.\n\n**REQ-b** (behavior): B MUST hold.\n\n---\n\nFree-standing context after the break.\n",
	}
	spec2, diags := compileFiles(t, afterHeading)
	wantClean(t, diags)
	if req(t, spec2, "REQ-a").GetContentHash() != origA.GetContentHash() {
		t.Fatal("a heading did not close REQ-a's extent")
	}

	// Past a thematic break: the free-standing context is not REQ-b's.
	afterBreak := map[string]string{
		"specs/a.md": "# Doc\n\n**REQ-a** (behavior): A MUST hold.\n\n## Section\n\nSection intro prose.\n\n**REQ-b** (behavior): B MUST hold.\n\n---\n\nEDITED free-standing context after the break.\n",
	}
	spec3, diags := compileFiles(t, afterBreak)
	wantClean(t, diags)
	if req(t, spec3, "REQ-b").GetContentHash() != origB.GetContentHash() {
		t.Fatal("a thematic break did not close REQ-b's extent")
	}

	// A note (blockquote) inside the extent rides the hash.
	withNote := map[string]string{
		"specs/a.md": "# Doc\n\n**REQ-a** (behavior): A MUST hold.\n\n> A clarifying note.\n\n## Section\n\nSection intro prose.\n\n**REQ-b** (behavior): B MUST hold.\n\n---\n\nFree-standing context after the break.\n",
	}
	spec4, diags := compileFiles(t, withNote)
	wantClean(t, diags)
	if req(t, spec4, "REQ-a").GetContentHash() == origA.GetContentHash() {
		t.Fatal("an attached note did not enter REQ-a's extent hash")
	}
}

// Terms carry extents under the same rule.
func TestContextExtentOnTerms(t *testing.T) {
	stipulate.Covers(t, "REQ-profile-context-extent")
	base := map[string]string{
		"specs/a.md": "# Doc\n\n**widget** (term): A widget is a unit of work.\n\nContext about widgets.\n",
	}
	spec, diags := compileFiles(t, base)
	wantClean(t, diags)
	var orig string
	for _, tm := range spec.GetTerms() {
		if tm.GetName() == "widget" {
			orig = tm.GetContentHash()
		}
	}
	if orig == "" {
		t.Fatal("term widget not compiled")
	}
	edited := map[string]string{
		"specs/a.md": "# Doc\n\n**widget** (term): A widget is a unit of work.\n\nEDITED context about widgets.\n",
	}
	spec2, diags := compileFiles(t, edited)
	wantClean(t, diags)
	for _, tm := range spec2.GetTerms() {
		if tm.GetName() == "widget" && tm.GetContentHash() == orig {
			t.Fatal("editing a term's context did not move its hash")
		}
	}
}

// A raw control byte in source cannot forge a block boundary: a lead
// containing a literal U+001E must not hash equal to a genuine
// lead+extent pair — canonicalization strips non-whitespace controls,
// so the delimiter is unrepresentable inside a part
// (REQ-model-content-hash's delimiter clause).
func TestRawSeparatorCannotForgeBoundary(t *testing.T) {
	stipulate.Covers(t, "REQ-model-content-hash")
	forge := map[string]string{
		"specs/a.md": "# Doc\n\n**REQ-a** (behavior): A MUST hold.\x1eExtra context words.\n",
	}
	genuine := map[string]string{
		"specs/a.md": "# Doc\n\n**REQ-a** (behavior): A MUST hold.\n\nExtra context words.\n",
	}
	specForge, _ := compileFiles(t, forge)
	specGen, diags := compileFiles(t, genuine)
	wantClean(t, diags)
	f, g := req(t, specForge, "REQ-a"), req(t, specGen, "REQ-a")
	if f.GetContentHash() == g.GetContentHash() {
		t.Fatal("a raw U+001E in source forged a block boundary — the delimiter survived canonicalization")
	}
	if strings.ContainsRune(f.GetText(), '\x1e') {
		t.Fatalf("carried text retains the control byte: %q", f.GetText())
	}
}
