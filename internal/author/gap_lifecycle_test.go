package author

import (
	"io/fs"
	"maps"
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

// The self sentinel resolves to each named requirement's own coverage —
// the design-stage idiom — while a literal target stays shared.
//
//gofresh:pure
func TestGapsSelfSentinel(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-bulk")
	fsys := testFS(nil)
	fsys["specs/b.md"] = &fstest.MapFile{Data: []byte(
		"# T\n\n**REQ-au-x** (behavior): It MUST x.\n\n**REQ-au-y** (behavior): It MUST y.\n")}
	lc, err := NewLandingCondition(SelfSentinel, "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	ups, _, err := Gaps(fsys, []string{"REQ-au-x", "REQ-au-y"}, "spec ahead of code", lc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 2 {
		t.Fatalf("updates = %d", len(ups))
	}
	for _, up := range ups {
		g := &stipulatorv1.Gap{}
		if err := prototext.Unmarshal(stripHeader(up.Content), g); err != nil {
			t.Fatal(err)
		}
		if g.GetLands().GetCovered() != g.GetRequirementId() {
			t.Errorf("%s lands on %q, want itself", g.GetRequirementId(), g.GetLands().GetCovered())
		}
	}
	// The sentinel must not leak into the caller's shared condition.
	if lc.GetCovered() != SelfSentinel {
		t.Errorf("shared condition mutated to %q", lc.GetCovered())
	}
	// The single form is the bulk form of one: the sentinel resolves on
	// the declaration path, not in the list walk.
	one := &stipulatorv1.Gap{}
	one.SetRequirementId("REQ-au-x")
	one.SetReason("spec ahead of code")
	one.SetLands(proto.CloneOf(lc))
	up, _, _, err := Gap(fsys, one)
	if err != nil {
		t.Fatal(err)
	}
	g := &stipulatorv1.Gap{}
	if err := prototext.Unmarshal(stripHeader(up.Content), g); err != nil {
		t.Fatal(err)
	}
	if g.GetLands().GetCovered() != "REQ-au-x" {
		t.Errorf("single form lands on %q, want itself", g.GetLands().GetCovered())
	}
}

// countingFS counts opens per path, so the bulk form's single compile
// and single store load are observable.
type countingFS struct {
	inner fs.FS // Open only, so every read funnels through the count
	opens map[string]int
}

func (c *countingFS) Open(name string) (fs.File, error) {
	c.opens[name]++
	return c.inner.Open(name)
}

// The bulk form compiles the corpus and loads the store once for the
// whole list: each declaration reads the same compiled spec.
//
//gofresh:pure
func TestGapsCompileOnce(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-bulk")
	m := testFS(nil)
	m["specs/b.md"] = &fstest.MapFile{Data: []byte(
		"# T\n\n**REQ-au-x** (behavior): It MUST x.\n\n**REQ-au-y** (behavior): It MUST y.\n\n**REQ-au-z** (behavior): It MUST z.\n")}
	lc, err := NewLandingCondition(SelfSentinel, "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	// The reads a declaration performs do not scale with the list: a
	// three-requirement declaration opens every corpus and store path
	// exactly as often as a one-requirement declaration does.
	one := &countingFS{inner: m, opens: map[string]int{}}
	if _, _, err := Gaps(one, []string{"REQ-au-x"}, "spec ahead of code", lc, nil); err != nil {
		t.Fatal(err)
	}
	three := &countingFS{inner: m, opens: map[string]int{}}
	if _, _, err := Gaps(three, []string{"REQ-au-x", "REQ-au-y", "REQ-au-z"}, "spec ahead of code", lc, nil); err != nil {
		t.Fatal(err)
	}
	if n := three.opens["specs/b.md"]; n != 1 {
		t.Fatalf("corpus document opened %d times for a three-requirement declaration, want 1", n)
	}
	if !maps.Equal(one.opens, three.opens) {
		t.Fatalf("opens scale with the list:\none  = %v\nthree = %v", one.opens, three.opens)
	}
}

// Retraction deletes records — dangling ones included, since the
// dangling state is what retraction repairs — never touching the
// tombstone registry, erroring on a requirement with no record, and
// applying all-or-nothing.
//
//gofresh:pure
func TestRetractGaps(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-retract")
	fsys := testFS(map[string]string{
		".stipulator/gaps/a.textproto": "requirement_id: \"REQ-au-a\"\nreason: \"r\"\nlands { exists: \"REQ-au-a\" }\n",
		// Dangling: REQ-gone-entirely is in no spec document.
		".stipulator/gaps/gone.textproto":  "requirement_id: \"REQ-gone-entirely\"\nreason: \"r\"\nlands { manual { condition: \"c\" } }\n",
		".stipulator/tombstones.textproto": "retired: \"REQ-old-thing\"\n",
	})
	ups, err := RetractGaps(fsys, []string{"REQ-au-a", "REQ-gone-entirely"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 2 {
		t.Fatalf("updates = %+v", ups)
	}
	for _, up := range ups {
		if up.Content != nil || !strings.HasPrefix(up.Path, records.GapsDir) {
			t.Fatalf("retraction wrote outside the gap store or kept content: %+v", up)
		}
	}
	if _, err := RetractGaps(fsys, []string{"REQ-au-a", "REQ-au-nogap"}); err == nil {
		t.Fatal("missing record mid-batch retracted anyway")
	}
	if _, err := RetractGaps(fsys, []string{"REQ-au-a", "REQ-au-a"}); err == nil {
		t.Fatal("duplicate requirement accepted")
	}
	if _, err := RetractGaps(fsys, nil); err == nil {
		t.Fatal("empty list accepted")
	}
}

// Firing marks an existing manual condition fired through the validated
// path: a machine condition refuses, a missing record refuses, a
// dangling record refuses toward retraction, an already-fired record
// stays fired, and the batch validates all-or-nothing.
//
//gofresh:pure
func TestFireGaps(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-verb")
	fsys := testFS(map[string]string{
		".stipulator/gaps/a.textproto": "requirement_id: \"REQ-au-a\"\nreason: \"r\"\nlands { manual { condition: \"judged done\" } }\n",
		".stipulator/gaps/b.textproto": "requirement_id: \"REQ-au-b\"\nreason: \"r\"\nlands { manual { condition: \"done\" fired: true } }\n",
	})
	ups, err := FireGaps(fsys, []string{"REQ-au-a", "REQ-au-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 2 {
		t.Fatalf("updates = %d", len(ups))
	}
	for _, up := range ups {
		g := &stipulatorv1.Gap{}
		if err := prototext.Unmarshal(stripHeader(up.Content), g); err != nil {
			t.Fatal(err)
		}
		if !g.GetLands().GetManual().GetFired() {
			t.Errorf("%s not fired:\n%s", g.GetRequirementId(), up.Content)
		}
		if g.GetReason() != "r" {
			t.Errorf("%s reason mangled: %q", g.GetRequirementId(), g.GetReason())
		}
	}
	machine := testFS(map[string]string{
		".stipulator/gaps/a.textproto": "requirement_id: \"REQ-au-a\"\nreason: \"r\"\nlands { covered: \"REQ-au-b\" }\n",
	})
	if _, err := FireGaps(machine, []string{"REQ-au-a"}); err == nil {
		t.Fatal("machine condition fired")
	}
	if _, err := FireGaps(fsys, []string{"REQ-au-a", "REQ-au-nogap"}); err == nil {
		t.Fatal("missing record mid-batch fired anyway")
	}
	// A dangling record's repair is retraction: firing validates the
	// requirement against the corpus exactly as declaring does.
	dangling := testFS(map[string]string{
		".stipulator/gaps/ghost.textproto": "requirement_id: \"REQ-au-ghost\"\nreason: \"r\"\nlands { manual { condition: \"c\" } }\n",
	})
	if _, err := FireGaps(dangling, []string{"REQ-au-ghost"}); err == nil || !strings.Contains(err.Error(), "retraction") {
		t.Fatalf("dangling fire error = %v, want the retraction pointer", err)
	}
}

// Re-declaring a gap whose manual condition text is unchanged preserves
// its fired state — an unfire is a lifecycle retarget that only happens
// through an explicit changed declaration.
//
//gofresh:pure
func TestGapFiredPreservedOnRedeclare(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-verb")
	fsys := testFS(map[string]string{
		".stipulator/gaps/a.textproto": "requirement_id: \"REQ-au-a\"\nreason: \"old\"\nlands { manual { condition: \"judged done\" fired: true } }\n",
	})
	redeclare := func(condition string) *stipulatorv1.Gap {
		g := &stipulatorv1.Gap{}
		g.SetRequirementId("REQ-au-a")
		g.SetReason("new reason")
		lc, err := NewLandingCondition("", "", condition, false, false)
		if err != nil {
			t.Fatal(err)
		}
		g.SetLands(lc)
		return g
	}
	up, _, _, err := Gap(fsys, redeclare("judged done"))
	if err != nil {
		t.Fatal(err)
	}
	got := &stipulatorv1.Gap{}
	if err := prototext.Unmarshal(stripHeader(up.Content), got); err != nil {
		t.Fatal(err)
	}
	if !got.GetLands().GetManual().GetFired() {
		t.Fatalf("unchanged condition silently unfired:\n%s", up.Content)
	}
	// The bulk surface surfaces the preservation when it overrides an
	// explicitly unfired declaration — after preservation the conditions
	// compare equal, so the ordinary retarget note cannot fire.
	lcUnfired, err := NewLandingCondition("", "", "judged done", false, false)
	if err != nil {
		t.Fatal(err)
	}
	_, notes, err := Gaps(fsys, []string{"REQ-au-a"}, "new reason", lcUnfired, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "fired state preserved") {
		t.Fatalf("preservation not surfaced: %v", notes)
	}
	up, _, _, err = Gap(fsys, redeclare("a different judgment"))
	if err != nil {
		t.Fatal(err)
	}
	got = &stipulatorv1.Gap{}
	if err := prototext.Unmarshal(stripHeader(up.Content), got); err != nil {
		t.Fatal(err)
	}
	if got.GetLands().GetManual().GetFired() {
		t.Fatalf("changed condition kept the old firing:\n%s", up.Content)
	}
}

// The contradicted class rides only a manual condition — a contradicted
// letter has no coverage-defined terminal — round-trips on the record,
// renders in the condition's spelling, and flipping it on re-declaration
// is a landing retarget surfaced like any other, the fired state still
// preserved across an unchanged condition text (REQ-gap-conditions,
// REQ-gap-verb, REQ-gap-record).
//
//gofresh:pure
func TestGapContradictedClassRidesTheManualCondition(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-conditions", "REQ-gap-verb", "REQ-gap-record")
	for _, machine := range [][2]string{{"REQ-au-a", ""}, {"", "REQ-au-a"}} {
		if _, err := NewLandingCondition(machine[0], machine[1], "", false, true); err == nil || !strings.Contains(err.Error(), "contradicted accompanies a manual condition") {
			t.Fatalf("contradicted with a machine condition %v = %v, want refused", machine, err)
		}
	}
	lc, err := NewLandingCondition("", "", "the derivation lands", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !lc.GetManual().GetContradicted() || LandingConditionString(lc) != "manual(the derivation lands) [contradicted]" {
		t.Fatalf("condition = %s / %+v, want the class carried and rendered", LandingConditionString(lc), lc)
	}
	fsys := testFS(nil)
	ups, notes, err := Gaps(fsys, []string{"REQ-au-a"}, "the shipped schema contradicts the letter", lc, nil)
	if err != nil || len(notes) != 0 {
		t.Fatalf("declare = %v %v", notes, err)
	}
	if !strings.Contains(string(ups[0].Content), "contradicted: true") {
		t.Fatalf("record lacks the class:\n%s", ups[0].Content)
	}
	fsys[ups[0].Path] = &fstest.MapFile{Data: ups[0].Content}
	// Fire it: the class stays, the condition fires.
	fired, err := FireGaps(fsys, []string{"REQ-au-a"})
	if err != nil {
		t.Fatal(err)
	}
	if c := string(fired[0].Content); !strings.Contains(c, "contradicted: true") || !strings.Contains(c, "fired: true") {
		t.Fatalf("fire dropped a field:\n%s", c)
	}
	fsys[fired[0].Path] = &fstest.MapFile{Data: fired[0].Content}
	// Dropping the class on re-declaration is a retarget, never silent,
	// and the unchanged condition text keeps its fired state.
	plain, err := NewLandingCondition("", "", "the derivation lands", false, false)
	if err != nil {
		t.Fatal(err)
	}
	ups, notes, err = Gaps(fsys, []string{"REQ-au-a"}, "re-judged", plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The flip is a retarget (the conditions compare unequal), so the
	// preservation note is the retarget's right-hand side, not a second
	// note.
	if len(notes) != 1 || !strings.Contains(notes[0], "landing retargeted manual(the derivation lands) [contradicted] [fired] -> manual(the derivation lands) [fired]") {
		t.Fatalf("class flip not surfaced as a retarget: %v", notes)
	}
	if c := string(ups[0].Content); strings.Contains(c, "contradicted") || !strings.Contains(c, "fired: true") {
		t.Fatalf("re-declaration = %s, want the class gone and the firing preserved", c)
	}
}

// Re-declaring an unchanged UNFIRED manual gap is silent: the built
// condition must not carry explicit fired=false presence, which would
// make proto.Equal see a retarget against every prior record that
// simply lacks the field.
//
//gofresh:pure
func TestUnchangedRedeclareIsSilent(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-verb")
	fsys := testFS(map[string]string{
		".stipulator/gaps/a.textproto": "requirement_id: \"REQ-au-a\"\nreason: \"r\"\nlands { manual { condition: \"c\" } }\n",
	})
	lc, err := NewLandingCondition("", "", "c", false, false)
	if err != nil {
		t.Fatal(err)
	}
	_, notes, err := Gaps(fsys, []string{"REQ-au-a"}, "r2", lc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("unchanged condition produced notes: %v", notes)
	}
}

// Fired at declaration time rides only a manual condition.
//
//gofresh:pure
func TestNewLandingConditionFired(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-verb")
	if _, err := NewLandingCondition("REQ-au-a", "", "", true, false); err == nil {
		t.Fatal("fired accepted on a machine condition")
	}
	lc, err := NewLandingCondition("", "", "external", true, false)
	if err != nil || !lc.GetManual().GetFired() {
		t.Fatalf("declare-fired: %v %v", lc, err)
	}
}

// stripHeader drops the leading #-comment header so prototext can parse
// a rendered record.
func stripHeader(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return []byte(strings.Join(out, "\n"))
}

// A batch's later claims validate against the earlier claims' pending
// writes: a duplicate inside one batch is refused exactly like a
// committed duplicate, which proves the overlay feeds each claim's
// effect forward — and a refusal anywhere authors nothing.
//
//gofresh:pure
func TestBindsBatchOverlay(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools")
	claims := []BindRequest{
		{Requirement: "REQ-au-a", Symbol: "example.com/p.F", Backend: "go", Role: stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS},
		{Requirement: "REQ-au-b", Symbol: "example.com/p.TestB", Backend: "go", Role: stipulatorv1.BindingRole_BINDING_ROLE_TESTS},
	}
	ups, err := Binds(testFS(nil), nil, claims)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 {
		t.Fatalf("same-file claims did not merge: %+v", ups)
	}
	if c := string(ups[0].Content); !strings.Contains(c, "REQ-au-a") || !strings.Contains(c, "REQ-au-b") {
		t.Fatalf("merged file misses a claim:\n%s", c)
	}
	dup := append(claims[:1:1], claims[0])
	if _, err := Binds(testFS(nil), nil, dup); err == nil || !strings.Contains(err.Error(), "identical binding already exists") {
		t.Fatalf("in-batch duplicate accepted: %v", err)
	}
	if _, err := Binds(testFS(nil), nil, nil); err == nil {
		t.Fatal("empty batch accepted")
	}
}

// The declaration stamps the requirement's current content hash — the
// consent surface, exactly as a binding's. A machine-evaluable landing
// target that does not match the identifier grammar refuses at write
// time, pointing prose at a manual condition; a well-formed identifier
// absent from the corpus is accepted — the prospective use covered and
// exists exist for — and surfaced in the notes so a typo is loud
// without foreclosing the future requirement.
//
//gofresh:pure
func TestGapDeclarationStampsConsentAndValidatesTargets(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-consent", "REQ-gap-verb")
	fsys := testFS(nil)
	fsys["specs/b.md"] = &fstest.MapFile{Data: []byte(
		"# T\n\n**REQ-au-x** (behavior): It MUST x.\n\n**REQ-au-y** (behavior): It MUST y.\n")}
	lc, err := NewLandingCondition("REQ-au-y", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	g := &stipulatorv1.Gap{}
	g.SetRequirementId("REQ-au-x")
	g.SetReason("spec ahead of code")
	g.SetLands(lc)
	up, _, notes, err := Gap(fsys, g)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("in-corpus target drew notes: %v", notes)
	}
	got := &stipulatorv1.Gap{}
	if err := prototext.Unmarshal(stripHeader(up.Content), got); err != nil {
		t.Fatal(err)
	}
	if len(got.GetContentHash()) != 64 {
		t.Fatalf("declaration did not stamp a content pin: %q", got.GetContentHash())
	}

	// A free-text covered target is refused at declaration — a gap
	// whose condition can never evaluate would stand permanently open.
	bad := &stipulatorv1.Gap{}
	bad.SetRequirementId("REQ-au-x")
	bad.SetReason("r")
	blc, _ := NewLandingCondition("the ingest plan's first chunk", "", "", false, false)
	bad.SetLands(blc)
	if _, _, _, err := Gap(fsys, bad); err == nil || !strings.Contains(err.Error(), "identifier grammar") || !strings.Contains(err.Error(), "manual") {
		t.Fatalf("free-text covered target = %v, want a grammar refusal naming manual", err)
	}

	// A well-formed identifier the corpus does not (yet) hold is the
	// prospective case: accepted, with a note naming the wait.
	prosp := &stipulatorv1.Gap{}
	prosp.SetRequirementId("REQ-au-x")
	prosp.SetReason("r")
	elc, _ := NewLandingCondition("", "REQ-not-yet-authored", "", false, false)
	prosp.SetLands(elc)
	pup, _, pnotes, err := Gap(fsys, prosp)
	if err != nil {
		t.Fatalf("prospective exists target refused: %v", err)
	}
	if pup == nil {
		t.Fatal("prospective declaration wrote nothing")
	}
	if len(pnotes) != 1 || !strings.Contains(pnotes[0], "exists(REQ-not-yet-authored)") || !strings.Contains(pnotes[0], "names no current requirement") {
		t.Fatalf("prospective target note = %v, want the unknown-target surfacing", pnotes)
	}
}

// A re-declaration over a drifted consent re-stamps — a fresh consent —
// and the discharge is surfaced in the notes, never silent.
//
//gofresh:pure
func TestGapRedeclarationSurfacesConsentRestamp(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-verb", "REQ-gap-consent")
	fsys := testFS(nil)
	fsys["specs/b.md"] = &fstest.MapFile{Data: []byte(
		"# T\n\n**REQ-au-x** (behavior): It MUST x.\n")}
	stale := strings.Repeat("0", 64)
	fsys[".stipulator/gaps/au-x.textproto"] = &fstest.MapFile{Data: []byte(
		"requirement_id: \"REQ-au-x\"\nreason: \"r\"\ncontent_hash: \"" + stale + "\"\nlands { manual { condition: \"ops\" } }\n")}
	g := &stipulatorv1.Gap{}
	g.SetRequirementId("REQ-au-x")
	g.SetReason("updated reason")
	mlc, _ := NewLandingCondition("", "", "ops", false, false)
	g.SetLands(mlc)
	up, _, notes, err := Gap(fsys, g)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n, "consent re-stamped") {
			found = true
		}
	}
	if !found {
		t.Fatalf("drift discharge not surfaced: notes = %v", notes)
	}
	got := &stipulatorv1.Gap{}
	if err := prototext.Unmarshal(stripHeader(up.Content), got); err != nil {
		t.Fatal(err)
	}
	if got.GetContentHash() == stale || len(got.GetContentHash()) != 64 {
		t.Fatalf("re-declaration did not re-stamp: %q", got.GetContentHash())
	}

	// A body comment in the existing record refuses the rewrite rather
	// than being destroyed (REQ-evidence-binding-machine-owned).
	fsys[".stipulator/gaps/au-x.textproto"] = &fstest.MapFile{Data: []byte(
		"requirement_id: \"REQ-au-x\"\nreason: \"r\"\n# why: operator note\nlands { manual { condition: \"ops\" } }\n")}
	if _, _, _, err := Gap(fsys, g); err == nil || !strings.Contains(err.Error(), "comment outside the leading header") {
		t.Fatalf("commented record rewrite = %v, want the machine-owned refusal", err)
	}
}

// The editorial disposition re-stamps the identity's gap record beside
// its bindings: the gap's consent surface rides the same ceremony.
//
//gofresh:pure
func TestEditorialRestampsGapPin(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-consent", "REQ-change-editorial")
	fsys := testFS(nil)
	fsys["specs/b.md"] = &fstest.MapFile{Data: []byte(
		"# T\n\n**REQ-au-x** (behavior): It MUST x.\n")}
	stale := strings.Repeat("0", 64)
	fsys[".stipulator/gaps/au-x.textproto"] = &fstest.MapFile{Data: []byte(
		"requirement_id: \"REQ-au-x\"\nreason: \"r\"\ncontent_hash: \"" + stale + "\"\nlands { manual { condition: \"ops\" } }\n")}
	ups, _, err := Editorial(fsys, "REQ-au-x")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, up := range ups {
		if up.Path != ".stipulator/gaps/au-x.textproto" {
			continue
		}
		g := &stipulatorv1.Gap{}
		if err := prototext.Unmarshal(stripHeader(up.Content), g); err != nil {
			t.Fatal(err)
		}
		if g.GetContentHash() == stale || len(g.GetContentHash()) != 64 {
			t.Fatalf("gap pin not re-stamped: %q", g.GetContentHash())
		}
		found = true
	}
	if !found {
		t.Fatal("editorial disposition did not touch the stale gap record")
	}

	// An UNSET pin is stamped by the per-identity ceremony too: an
	// explicit consent to the current text needs no pre-field grace
	// (REQ-gap-consent) — only the blanket form is restricted to
	// backfill.
	fsys[".stipulator/gaps/au-x.textproto"] = &fstest.MapFile{Data: []byte(
		"requirement_id: \"REQ-au-x\"\nreason: \"r\"\nlands { manual { condition: \"ops\" } }\n")}
	ups, _, err = Editorial(fsys, "REQ-au-x")
	if err != nil {
		t.Fatal(err)
	}
	stamped := false
	for _, up := range ups {
		if up.Path != ".stipulator/gaps/au-x.textproto" {
			continue
		}
		g := &stipulatorv1.Gap{}
		if err := prototext.Unmarshal(stripHeader(up.Content), g); err != nil {
			t.Fatal(err)
		}
		if len(g.GetContentHash()) == 64 {
			stamped = true
		}
	}
	if !stamped {
		t.Fatal("editorial disposition left the unset gap pin unstamped")
	}

	// A body comment refuses the rewrite rather than being destroyed
	// (REQ-evidence-binding-machine-owned).
	fsys[".stipulator/gaps/au-x.textproto"] = &fstest.MapFile{Data: []byte(
		"requirement_id: \"REQ-au-x\"\nreason: \"r\"\n# why: operator note\nlands { manual { condition: \"ops\" } }\n")}
	if _, _, err := Editorial(fsys, "REQ-au-x"); err == nil || !strings.Contains(err.Error(), "comment outside the leading header") {
		t.Fatalf("commented record re-pin = %v, want the machine-owned refusal", err)
	}
}

// Firing a gap rewrites the record through the machine-owned gap writer
// like every other rewrite: a body comment refuses, a custom header is
// preserved (REQ-evidence-binding-machine-owned).
//
//gofresh:pure
func TestFireGapsMachineOwnedRewrite(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-verb")
	fsys := testFS(nil)
	fsys["specs/b.md"] = &fstest.MapFile{Data: []byte(
		"# T\n\n**REQ-au-x** (behavior): It MUST x.\n")}
	fsys[".stipulator/gaps/au-x.textproto"] = &fstest.MapFile{Data: []byte(
		"requirement_id: \"REQ-au-x\"\nreason: \"r\"\n# why this is still open\nlands { manual { condition: \"ops\" } }\n")}
	if _, err := FireGaps(fsys, []string{"REQ-au-x"}); err == nil || !strings.Contains(err.Error(), "comment outside the leading header") {
		t.Fatalf("firing a commented record = %v, want the machine-owned refusal", err)
	}
	fsys[".stipulator/gaps/au-x.textproto"] = &fstest.MapFile{Data: []byte(
		"# proto-file: custom/path.proto\n# proto-message: stipulator.v1.Gap\nrequirement_id: \"REQ-au-x\"\nreason: \"r\"\nlands { manual { condition: \"ops\" } }\n")}
	ups, err := FireGaps(fsys, []string{"REQ-au-x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 {
		t.Fatalf("fired updates = %d, want 1", len(ups))
	}
	if !strings.Contains(string(ups[0].Content), "# proto-file: custom/path.proto") || !strings.Contains(string(ups[0].Content), "fired: true") {
		t.Fatalf("fire rewrite lost the header or the fired bit:\n%s", ups[0].Content)
	}
}
