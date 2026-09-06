package records

import (
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
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
	ups, preserved, _, _, err := Pin(store, Hashes{Content: map[string]string{"REQ-r-a": current, "REQ-r-b": current}}, nil)
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
	ups, _, reshaped, _, err := Pin(store, Hashes{}, map[string]string{
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

// TestPinBackfillsGapPins pins the gap side of the blanket discipline:
// an unset gap content pin backfills to the current hash, a differing
// one is preserved and its requirement named, and a current one leaves
// the file untouched.
//
//gofresh:pure
func TestPinBackfillsGapPins(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-consent", "REQ-pin-backfill")
	current := strings.Repeat("a", 64)
	drifted := strings.Repeat("0", 64)
	gapFile := func(id, hash string) GapFile {
		g := &stipulatorv1.Gap{}
		g.SetRequirementId(id)
		g.SetReason("r")
		if hash != "" {
			g.SetContentHash(hash)
		}
		lc := &stipulatorv1.LandingCondition{}
		m := &stipulatorv1.ManualCondition{}
		m.SetCondition("external")
		lc.SetManual(m)
		g.SetLands(lc)
		return GapFile{Path: ".stipulator/gaps/" + id + ".textproto", Gap: g}
	}
	store := &Store{Gaps: []GapFile{
		gapFile("REQ-pg-unset", ""),
		gapFile("REQ-pg-drift", drifted),
		gapFile("REQ-pg-current", current),
	}}
	hashes := map[string]string{
		"REQ-pg-unset": current, "REQ-pg-drift": current, "REQ-pg-current": current,
	}
	out, preserved, _, _, err := Pin(store, Hashes{Content: hashes}, nil)
	if err != nil {
		t.Fatal(err)
	}
	backfilled, ok := out[".stipulator/gaps/REQ-pg-unset.textproto"]
	if !ok || !strings.Contains(string(backfilled), current) {
		t.Fatalf("unset gap pin not backfilled: %q", backfilled)
	}
	// A record with no leading header (legal on disk) gains the standard
	// one through the rewrite — it must never come out headerless or
	// blank-led (REQ-evidence-binding-machine-owned).
	if !strings.HasPrefix(string(backfilled), "# proto-file:") {
		t.Fatalf("headerless record rewrote without the standard header:\n%s", backfilled)
	}
	if _, ok := out[".stipulator/gaps/REQ-pg-drift.textproto"]; ok {
		t.Fatal("a differing gap pin was rewritten; staleness laundered")
	}
	if _, ok := out[".stipulator/gaps/REQ-pg-current.textproto"]; ok {
		t.Fatal("a current gap pin rewrote its file")
	}
	found := false
	for _, id := range preserved {
		if id == "REQ-pg-drift" {
			found = true
		}
	}
	if !found {
		t.Fatalf("preserved differing gap pin not named: %v", preserved)
	}

	// A gap file carrying a comment outside the leading header refuses
	// the backfill rewrite rather than destroying the commentary — and a
	// custom HEADER is preserved through it, both exactly as a binding
	// file's (REQ-evidence-binding-machine-owned).
	commented := gapFile("REQ-pg-commented", "")
	commented.Raw = []byte("requirement_id: \"REQ-pg-commented\"\nreason: \"r\"\n# why: operator note\nlands { manual { condition: \"external\" } }\n")
	if _, _, _, _, err := Pin(&Store{Gaps: []GapFile{commented}}, Hashes{Content: map[string]string{"REQ-pg-commented": current}}, nil); err == nil || !strings.Contains(err.Error(), "comment outside the leading header") {
		t.Fatalf("commented gap backfill = %v, want the machine-owned refusal", err)
	}
	headered := gapFile("REQ-pg-headered", "")
	headered.Raw = []byte("# proto-file: custom/path.proto\n# proto-message: stipulator.v1.Gap\nrequirement_id: \"REQ-pg-headered\"\nreason: \"r\"\nlands { manual { condition: \"external\" } }\n")
	hout, _, _, _, err := Pin(&Store{Gaps: []GapFile{headered}}, Hashes{Content: map[string]string{"REQ-pg-headered": current}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rew := string(hout[".stipulator/gaps/REQ-pg-headered.textproto"])
	if !strings.Contains(rew, "# proto-file: custom/path.proto") || !strings.Contains(rew, current) {
		t.Fatalf("header not preserved through the backfill rewrite:\n%s", rew)
	}
}

// A record whose content pin differs while its source pin matches the
// requirement's consent-source digest consented to byte-identical text:
// the blanket form rewrites its content pin — a rehash, no consent
// question — and names the requirement; a current record with no
// source pin gains one; a differing content pin with a differing (or
// absent) source pin stays preserved. Gaps and attestations ride the
// same pass (REQ-evidence-consent-current, REQ-pin-backfill).
//
//gofresh:pure
func TestPinRehashesByteIdenticalText(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-consent-current", "REQ-pin-backfill")
	old := strings.Repeat("0", 64)
	current := strings.Repeat("1", 64)
	src := strings.Repeat("5", 64)
	otherSrc := strings.Repeat("6", 64)
	rec := func(id, sym, content, source string) string {
		b := "bindings {\n  requirement_id: \"" + id + "\"\n  backend: \"go\"\n  symbol: \"" + sym + "\"\n  role: BINDING_ROLE_IMPLEMENTS\n  content_hash: \"" + content + "\"\n"
		if source != "" {
			b += "  source_hash: \"" + source + "\"\n"
		}
		return b + "}\n"
	}
	store := storeWith(t,
		rec("REQ-r-rehash", "example.com/p.A", old, src)+ // rehash
			rec("REQ-r-moved", "example.com/p.B", old, otherSrc)+ // text moved: preserved
			rec("REQ-r-prefield", "example.com/p.C", old, "")+ // pre-field, differing: preserved
			rec("REQ-r-current", "example.com/p.D", current, "")+ // current, gains its source pin
			rec("REQ-r-rewrapped", "example.com/p.E", current, otherSrc)) // current, source refreshed
	gap := &stipulatorv1.Gap{}
	gap.SetRequirementId("REQ-r-rehash")
	gap.SetReason("r")
	gap.SetContentHash(old)
	gap.SetSourceHash(src)
	lc := &stipulatorv1.LandingCondition{}
	m := &stipulatorv1.ManualCondition{}
	m.SetCondition("c")
	lc.SetManual(m)
	gap.SetLands(lc)
	store.Gaps = append(store.Gaps, GapFile{Path: ".stipulator/gaps/rehash.textproto", Gap: gap})
	att := &stipulatorv1.RequirementAttestation{}
	att.SetRequirementId("REQ-r-rehash")
	att.SetContentHash(old)
	att.SetSourceHash(src)
	att.SetReason("judged")
	aset := &stipulatorv1.AttestationSet{}
	aset.SetAttestations([]*stipulatorv1.RequirementAttestation{att})
	store.Attestations = append(store.Attestations, AttestationFile{Path: ".stipulator/attestations/rehash.textproto", Raw: append([]byte("# reviewed by hand, keep this note\n"), RenderAttestations(aset)...), Set: aset})
	hashes := Hashes{
		Content: map[string]string{"REQ-r-rehash": current, "REQ-r-moved": current, "REQ-r-prefield": current, "REQ-r-current": current, "REQ-r-rewrapped": current},
		Source:  map[string]string{"REQ-r-rehash": src, "REQ-r-moved": src, "REQ-r-prefield": src, "REQ-r-current": src, "REQ-r-rewrapped": src},
	}
	ups, preserved, _, rehashed, err := Pin(store, hashes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rehashed) != 1 || rehashed[0] != "REQ-r-rehash" {
		t.Fatalf("rehashed = %v, want [REQ-r-rehash]", rehashed)
	}
	if len(preserved) != 2 || preserved[0] != "REQ-r-moved" || preserved[1] != "REQ-r-prefield" {
		t.Fatalf("preserved = %v, want the moved and the pre-field differing pins", preserved)
	}
	bindings := string(ups[".stipulator/bindings/t.textproto"])
	if !strings.Contains(bindings, "content_hash: \""+current+"\"\n  source_hash: \""+src+"\"\n  backend: \"go\"\n  symbol: \"example.com/p.A\"") {
		t.Fatalf("the rehash was not rewritten to the current hash:\n%s", bindings)
	}
	if strings.Count(bindings, "content_hash: \""+old+"\"") != 2 {
		t.Fatalf("preserved pins were rewritten:\n%s", bindings)
	}
	if !strings.Contains(bindings, "content_hash: \""+current+"\"\n  source_hash: \""+src+"\"\n  backend: \"go\"\n  symbol: \"example.com/p.D\"") {
		t.Fatalf("the current record did not gain its source pin:\n%s", bindings)
	}
	// A current content pin proves consent to the current text, so a
	// source pin a rewrap left behind is refreshed — otherwise the next
	// canonical-form move would read stale instead of rehash.
	if !strings.Contains(bindings, "content_hash: \""+current+"\"\n  source_hash: \""+src+"\"\n  backend: \"go\"\n  symbol: \"example.com/p.E\"") {
		t.Fatalf("the rewrapped record's source pin was not refreshed:\n%s", bindings)
	}
	if g := string(ups[".stipulator/gaps/rehash.textproto"]); !strings.Contains(g, "content_hash: \""+current+"\"") {
		t.Fatalf("the gap's rehash was not rewritten:\n%s", g)
	}
	if a := string(ups[".stipulator/attestations/rehash.textproto"]); !strings.Contains(a, "content_hash: \""+current+"\"") || !strings.Contains(a, "source_hash: \""+src+"\"") || !strings.HasPrefix(a, "# reviewed by hand, keep this note\n") {
		t.Fatalf("the attestation's rehash was not rewritten with its header preserved:\n%s", a)
	}
	// Idempotent: nothing left to rewrite once rehashed and backfilled.
	again, _, _, rehashedAgain, err := Pin(store, hashes, nil)
	if err != nil || len(again) != 0 || len(rehashedAgain) != 0 {
		t.Fatalf("second pin = %v %v %v, want quiescence", again, rehashedAgain, err)
	}
}

// JudgeConsent is the one consent rule: current by content, by rehash
// when the source pin alone matches, stale otherwise — an unset content
// pin never current, an unset source pin never a rehash
// (REQ-evidence-consent-current).
//
//gofresh:pure
func TestJudgeConsent(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-consent-current")
	for _, c := range []struct {
		content, source, hash, digest string
		want                          Consent
	}{
		{"h", "s", "h", "s", Current},
		{"h", "", "h", "s", Current},
		{"h", "s", "h", "t", Current},
		{"g", "s", "h", "s", Rehash},
		{"g", "s", "h", "t", Stale},
		{"g", "", "h", "s", Stale},
		{"", "s", "h", "s", Stale},
		{"", "", "h", "", Stale},
		{"g", "", "h", "", Stale},
	} {
		if got := JudgeConsent(c.content, c.source, c.hash, c.digest); got != c.want {
			t.Errorf("JudgeConsent(%q,%q,%q,%q) = %v, want %v", c.content, c.source, c.hash, c.digest, got, c.want)
		}
	}
	if Stale.Holds() || !Current.Holds() || !Rehash.Holds() {
		t.Fatal("Holds: stale must not, current and rehash must")
	}
}
