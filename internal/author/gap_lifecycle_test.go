package author

import (
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/prototext"
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
	lc, err := NewLandingCondition(SelfSentinel, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	ups, _, err := Gaps(fsys, []string{"REQ-au-x", "REQ-au-y"}, "spec ahead of code", lc)
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
		lc, err := NewLandingCondition("", "", condition, false)
		if err != nil {
			t.Fatal(err)
		}
		g.SetLands(lc)
		return g
	}
	up, _, err := Gap(fsys, redeclare("judged done"))
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
	lcUnfired, err := NewLandingCondition("", "", "judged done", false)
	if err != nil {
		t.Fatal(err)
	}
	_, notes, err := Gaps(fsys, []string{"REQ-au-a"}, "new reason", lcUnfired)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "fired state preserved") {
		t.Fatalf("preservation not surfaced: %v", notes)
	}
	up, _, err = Gap(fsys, redeclare("a different judgment"))
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

// Fired at declaration time rides only a manual condition.
//
//gofresh:pure
func TestNewLandingConditionFired(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-verb")
	if _, err := NewLandingCondition("REQ-au-a", "", "", true); err == nil {
		t.Fatal("fired accepted on a machine condition")
	}
	lc, err := NewLandingCondition("", "", "external", true)
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
