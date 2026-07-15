package author

import (
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/prototext"
)

const doc = "# T\n\n**REQ-au-a** (behavior): It MUST x.\n\n**REQ-au-b** (behavior): It MUST y.\n"

func testFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(doc)},
	}
	for p, c := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(c)}
	}
	return fsys
}

// fakeBackend: absent = NotFound, "GEN" = GeneratedFile, else Resolved.
type fakeBackend map[string]string

func (f fakeBackend) Resolve(symbol string) (verify.Resolution, string, error) {
	shape, ok := f[symbol]
	switch {
	case !ok:
		return verify.NotFound, "", nil
	case shape == "GEN":
		return verify.GeneratedFile, "", nil
	}
	return verify.Resolved, shape, nil
}

var backends = map[string]verify.Backend{"go": fakeBackend{
	"example.com/p.F":   strings.Repeat("s", 64),
	"example.com/p.Gen": "GEN",
}}

func bindReq(id, symbol string) BindRequest {
	return BindRequest{
		Requirement: id, Symbol: symbol, Backend: "go",
		Role: stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS,
	}
}

//gofresh:pure
func TestBind(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-record-verbs")

	t.Run("authors a fully pinned binding into the derived file", func(t *testing.T) {
		up, err := Bind(testFS(nil), backends, bindReq("REQ-au-a", "example.com/p.F"))
		if err != nil {
			t.Fatal(err)
		}
		if up.Path != ".stipulator/bindings/au.textproto" {
			t.Fatalf("path = %s", up.Path)
		}
		set := &stipulatorv1.BindingSet{}
		if err := prototext.Unmarshal(up.Content, set); err != nil {
			t.Fatalf("output does not parse: %v\n%s", err, up.Content)
		}
		b := set.GetBindings()[0]
		if len(b.GetContentHash()) != 64 || len(b.GetShapeHash()) != 64 {
			t.Fatalf("binding born unpinned: %v", b)
		}
	})

	t.Run("unknown requirement refused", func(t *testing.T) {
		_, err := Bind(testFS(nil), backends, bindReq("REQ-au-ghost", "example.com/p.F"))
		if err == nil || !strings.Contains(err.Error(), "not in the corpus") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("unresolved symbol refused", func(t *testing.T) {
		_, err := Bind(testFS(nil), backends, bindReq("REQ-au-a", "example.com/p.Gone"))
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("generated symbol refused", func(t *testing.T) {
		_, err := Bind(testFS(nil), backends, bindReq("REQ-au-a", "example.com/p.Gen"))
		if err == nil || !strings.Contains(err.Error(), "generated file") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("backend without verifier allowed, shape unpinned", func(t *testing.T) {
		req := bindReq("REQ-au-a", "some.v1.Message")
		req.Backend = "proto"
		up, err := Bind(testFS(nil), backends, req)
		if err != nil {
			t.Fatal(err)
		}
		set := &stipulatorv1.BindingSet{}
		if err := prototext.Unmarshal(up.Content, set); err != nil {
			t.Fatal(err)
		}
		b := set.GetBindings()[0]
		if len(b.GetContentHash()) != 64 || b.GetShapeHash() != "" {
			t.Fatalf("want content pinned, shape unpinned: %v", b)
		}
	})

	t.Run("identical binding refused", func(t *testing.T) {
		fsys := testFS(nil)
		up, err := Bind(fsys, backends, bindReq("REQ-au-a", "example.com/p.F"))
		if err != nil {
			t.Fatal(err)
		}
		fsys[up.Path] = &fstest.MapFile{Data: up.Content}
		if _, err := Bind(fsys, backends, bindReq("REQ-au-a", "example.com/p.F")); err == nil {
			t.Fatal("duplicate accepted")
		}
	})

	t.Run("appending preserves existing header", func(t *testing.T) {
		fsys := testFS(nil)
		up, _ := Bind(fsys, backends, bindReq("REQ-au-a", "example.com/p.F"))
		fsys[up.Path] = &fstest.MapFile{Data: up.Content}
		req := bindReq("REQ-au-b", "example.com/p.F")
		req.File = up.Path
		up2, err := Bind(fsys, backends, req)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(up2.Content), "# proto-file:") {
			t.Fatalf("header lost:\n%s", up2.Content)
		}
		set := &stipulatorv1.BindingSet{}
		if err := prototext.Unmarshal(up2.Content, set); err != nil || len(set.GetBindings()) != 2 {
			t.Fatalf("append failed: %v %v", err, set)
		}
	})

	t.Run("commented file refused", func(t *testing.T) {
		fsys := testFS(map[string]string{
			".stipulator/bindings/au.textproto": "# header\nbindings {\n  requirement_id: \"REQ-au-b\"\n  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_TESTS\n}\n# hand note\n",
		})
		req := bindReq("REQ-au-a", "example.com/p.F")
		req.File = ".stipulator/bindings/au.textproto"
		if _, err := Bind(fsys, backends, req); err == nil || !strings.Contains(err.Error(), "comment outside") {
			t.Fatalf("err = %v", err)
		}
	})
}

//gofresh:pure
func TestBindFileConfinement(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-record-verbs")
	for _, escape := range []string{"specs/a.md", "../evil.textproto", ".stipulator/bindings/../../x.textproto", ".stipulator/bindings/x.md"} {
		req := bindReq("REQ-au-a", "example.com/p.F")
		req.File = escape
		if _, err := Bind(testFS(nil), backends, req); err == nil {
			t.Fatalf("file escape accepted: %s", escape)
		}
	}
	req := bindReq("REQ-au-a", "example.com/p.F")
	req.Backend = "gp"
	if _, err := Bind(testFS(nil), backends, req); err == nil || !strings.Contains(err.Error(), "unknown backend") {
		t.Fatal("typo'd backend accepted")
	}
}

//gofresh:pure
func TestUnbind(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-record-verbs")
	fsys := testFS(nil)
	up, _ := Bind(fsys, backends, bindReq("REQ-au-a", "example.com/p.F"))
	fsys[up.Path] = &fstest.MapFile{Data: up.Content}

	t.Run("no match is an error", func(t *testing.T) {
		if _, _, err := Unbind(fsys, "REQ-au-b", "", 0); err == nil {
			t.Fatal("matched nothing yet succeeded")
		}
	})

	t.Run("removing the last binding deletes the file", func(t *testing.T) {
		ups, removed, err := Unbind(fsys, "REQ-au-a", "", 0)
		if err != nil || removed != 1 {
			t.Fatalf("removed=%d err=%v", removed, err)
		}
		if len(ups) != 1 || ups[0].Content != nil {
			t.Fatalf("want deletion, got %+v", ups)
		}
	})
}

//gofresh:pure
func TestGap(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-verb")
	mkGap := func(id, reason string, lands *stipulatorv1.LandingCondition) *stipulatorv1.Gap {
		g := &stipulatorv1.Gap{}
		g.SetRequirementId(id)
		g.SetReason(reason)
		if lands != nil {
			g.SetLands(lands)
		}
		return g
	}
	covered := func(id string) *stipulatorv1.LandingCondition {
		lc := &stipulatorv1.LandingCondition{}
		lc.SetCovered(id)
		return lc
	}

	t.Run("authors a parseable record at the canonical path", func(t *testing.T) {
		up, _, err := Gap(testFS(nil), mkGap("REQ-au-a", "later", covered("REQ-au-b")))
		if err != nil {
			t.Fatal(err)
		}
		if up.Path != ".stipulator/gaps/au-a.textproto" {
			t.Fatalf("path = %s", up.Path)
		}
		g := &stipulatorv1.Gap{}
		if err := prototext.Unmarshal(up.Content, g); err != nil {
			t.Fatalf("output does not parse: %v\n%s", err, up.Content)
		}
		if g.GetLands().GetCovered() != "REQ-au-b" {
			t.Fatalf("condition lost: %v", g)
		}
	})

	t.Run("validations", func(t *testing.T) {
		cases := []struct {
			name string
			gap  *stipulatorv1.Gap
			want string
		}{
			{"unknown requirement", mkGap("REQ-au-ghost", "r", covered("REQ-au-b")), "not in the corpus"},
			{"missing reason", mkGap("REQ-au-a", "", covered("REQ-au-b")), "reason is required"},
			{"missing condition", mkGap("REQ-au-a", "r", nil), "landing condition is required"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if _, _, err := Gap(testFS(nil), c.gap); err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("err = %v", err)
				}
			})
		}
	})

	t.Run("declaring over an existing gap updates it in place", func(t *testing.T) {
		fsys := testFS(nil)
		up, prior, err := Gap(fsys, mkGap("REQ-au-a", "not yet implemented", covered("REQ-au-b")))
		if err != nil || prior != nil {
			t.Fatalf("fresh gap: %v prior=%v", err, prior)
		}
		fsys[up.Path] = &fstest.MapFile{Data: up.Content}

		// Same landing, evolved reason: quiet update, prior surfaced.
		up2, prior2, err := Gap(fsys, mkGap("REQ-au-a", "implemented; awaiting witness", covered("REQ-au-b")))
		if err != nil || prior2 == nil || prior2.GetReason() != "not yet implemented" {
			t.Fatalf("update: %v prior=%v", err, prior2)
		}
		if up2.Path != up.Path || !strings.Contains(string(up2.Content), "awaiting witness") {
			t.Fatalf("update misplaced: %s\n%s", up2.Path, up2.Content)
		}

		// A retarget is surfaced through the bulk verb's notes.
		fsys[up2.Path] = &fstest.MapFile{Data: up2.Content}
		lc := &stipulatorv1.LandingCondition{}
		lc.SetExists("REQ-au-b")
		_, notes, err := Gaps(fsys, []string{"REQ-au-a"}, "retargeted", lc)
		if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "covered(REQ-au-b) -> exists(REQ-au-b)") {
			t.Fatalf("retarget not surfaced: %v %v", err, notes)
		}
	})

	t.Run("foreign record at the canonical path still refused", func(t *testing.T) {
		fsys := testFS(map[string]string{
			// REQ-au-b's record legally sits at REQ-au-a's canonical path.
			".stipulator/gaps/au-a.textproto": "requirement_id: \"REQ-au-b\"\nreason: \"r\"\nlands { exists: \"REQ-au-a\" }\n",
		})
		if _, _, err := Gap(fsys, mkGap("REQ-au-a", "r", covered("REQ-au-b"))); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
			t.Fatalf("foreign-path overwrite accepted: %v", err)
		}
	})

	t.Run("manual and exists render and parse", func(t *testing.T) {
		att := &stipulatorv1.LandingCondition{}
		a := &stipulatorv1.ManualCondition{}
		a.SetCondition("external thing")
		att.SetManual(a)
		up, _, err := Gap(testFS(nil), mkGap("REQ-au-b", "r", att))
		if err != nil {
			t.Fatal(err)
		}
		g := &stipulatorv1.Gap{}
		if err := prototext.Unmarshal(up.Content, g); err != nil {
			t.Fatal(err)
		}
		if g.GetLands().GetManual().GetCondition() != "external thing" || g.GetLands().GetManual().GetFired() {
			t.Fatalf("manual condition mangled: %v", g)
		}
	})
}

// The registrations above are backed by bindings authored — fittingly —
// with the bind verb itself.
var _ = records.GapsDir

//gofresh:pure
func TestParseRoleAndConditions(t *testing.T) {
	if _, err := ParseRole("implments"); err == nil {
		t.Fatal("typo'd role accepted — mass-removal hazard")
	}
	if r, err := ParseRole(""); err != nil || r != stipulatorv1.BindingRole_BINDING_ROLE_UNSPECIFIED {
		t.Fatalf("empty role = %v %v", r, err)
	}
	if _, err := NewLandingCondition("REQ-au-a", "", "also"); err == nil {
		t.Fatal("conflicting conditions accepted")
	}
	lc, err := NewLandingCondition("", "", "external")
	if err != nil || !lc.HasManual() {
		t.Fatalf("manual condition: %v %v", lc, err)
	}
}

//gofresh:pure
func TestGapRefusesForeignPathCollision(t *testing.T) {
	// A hand-authored gap for another requirement legally sitting at this
	// requirement's canonical path must never be overwritten.
	fsys := testFS(map[string]string{
		records.GapPath("REQ-au-a"): "requirement_id: \"REQ-au-b\"\nreason: \"r\"\nlands { exists: \"REQ-au-a\" }\n",
	})
	g := &stipulatorv1.Gap{}
	g.SetRequirementId("REQ-au-a")
	g.SetReason("r")
	lc := &stipulatorv1.LandingCondition{}
	lc.SetCovered("REQ-au-b")
	g.SetLands(lc)
	if _, _, err := Gap(fsys, g); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("err = %v", err)
	}
}

//gofresh:pure
func TestAppendPreservesIndentedHeaderComment(t *testing.T) {
	raw := "# header\n  # indented note, still header\n\nbindings {\n  requirement_id: \"REQ-au-b\"\n  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_TESTS\n}\n"
	fsys := testFS(map[string]string{".stipulator/bindings/au.textproto": raw})
	req := bindReq("REQ-au-a", "example.com/p.F")
	req.File = ".stipulator/bindings/au.textproto"
	up, err := Bind(fsys, backends, req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(up.Content), "indented note") {
		t.Fatalf("indented header comment silently destroyed:\n%s", up.Content)
	}
}

// TestInit pins first-run bootstrap: a fresh tree scaffolds the manifest
// with the default include; an initialized tree refuses.
//
//gofresh:pure
func TestInit(t *testing.T) {
	up, err := Init(fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	if up.Path != corpus.ManifestPath {
		t.Fatalf("path = %s", up.Path)
	}
	fsys := fstest.MapFS{up.Path: &fstest.MapFile{Data: up.Content}}
	if _, err := corpus.LoadManifest(fsys); err != nil {
		t.Fatalf("scaffolded manifest does not load: %v", err)
	}
	if _, err := Init(fsys); err == nil {
		t.Fatal("re-init accepted")
	}
}

// TestGapsBulk pins bulk declaration: one record per requirement, shared
// reason and landing condition, all-or-nothing validation.
//
//gofresh:pure
func TestGapsBulk(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-verb")
	fsys := testFS(nil)
	fsys["specs/a.md"] = &fstest.MapFile{Data: []byte(
		"# T\n\n**REQ-au-a** (behavior): It MUST x.\n\n**REQ-au-b** (behavior): It MUST y.\n")}
	lc, err := NewLandingCondition("", "", "later")
	if err != nil {
		t.Fatal(err)
	}
	ups, _, err := Gaps(fsys, []string{"REQ-au-a", "REQ-au-b"}, "spec ahead of code", lc)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 2 {
		t.Fatalf("updates = %d", len(ups))
	}
	for _, up := range ups {
		if !strings.Contains(string(up.Content), `reason: "spec ahead of code"`) {
			t.Fatalf("shared reason missing:\n%s", up.Content)
		}
	}
	if _, _, err := Gaps(fsys, []string{"REQ-au-a", "REQ-au-ghost"}, "r", lc); err == nil {
		t.Fatal("typo mid-list declared gaps anyway")
	}
	if _, _, err := Gaps(fsys, []string{"REQ-au-a", "REQ-au-a"}, "r", lc); err == nil {
		t.Fatal("duplicate requirement accepted")
	}
	if _, _, err := Gaps(fsys, nil, "r", lc); err == nil {
		t.Fatal("empty list accepted")
	}
}

// TestPruneResolvedGaps pins the fmt arm of gap hygiene: resolved gaps
// delete, open ones stay.
//
//gofresh:pure
func TestPruneResolvedGaps(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned")
	fsys := testFS(map[string]string{
		".stipulator/gaps/a.textproto": "requirement_id: \"REQ-au-a\"\nreason: \"r\"\nlands { manual { condition: \"x\" } }\n",
		".stipulator/gaps/b.textproto": "requirement_id: \"REQ-au-b\"\nreason: \"r\"\nlands { manual { condition: \"x\" } }\n",
	})
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	ups := PruneResolvedGaps(store, map[string]bool{"REQ-au-a": true})
	if len(ups) != 1 || ups[0].Path != ".stipulator/gaps/a.textproto" || ups[0].Content != nil {
		t.Fatalf("prunes = %+v", ups)
	}
	both := PruneResolvedGaps(store, map[string]bool{"REQ-au-a": true, "REQ-au-b": true})
	if len(both) != 2 || !(both[0].Path < both[1].Path) {
		t.Fatalf("prunes unordered or incomplete: %+v", both)
	}
}

// fakeClassifier resolves a fixed witness class alongside resolution.
type fakeClassifier struct {
	fakeBackend
	class verify.WitnessClass
}

func (f fakeClassifier) WitnessClass(string) verify.WitnessClass { return f.class }

type fakeSyntaxVerdict struct {
	fakeBackend
}

func (fakeSyntaxVerdict) Vacuous(string) (bool, error) { return true, nil }

// TestBindDoesNotInferTestOutcomeFromSyntax pins the evidence boundary:
// successful resolution authors the claim, while execution decides whether
// the bound test produces evidence.
//
//gofresh:pure
func TestBindDoesNotInferTestOutcomeFromSyntax(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-record-verbs", "REQ-evidence-promotion")
	fsys := testFS(nil)
	req := bindReq("REQ-au-a", "example.com/p.F")
	req.Role = stipulatorv1.BindingRole_BINDING_ROLE_TESTS
	be := fakeSyntaxVerdict{fakeBackend{"example.com/p.F": strings.Repeat("s", 64)}}
	if _, err := Bind(fsys, map[string]verify.Backend{"go": be}, req); err != nil {
		t.Fatalf("resolved test refused by syntax verdict: %v", err)
	}
}

// TestProvesDischarge pins the loud-failure contract: a proves claim the
// backend cannot discharge is refused at write time, never recorded.
//
//gofresh:pure
func TestProvesDischarge(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-proves-discharge")
	fsys := testFS(nil)
	req := bindReq("REQ-au-a", "example.com/p.F")
	req.Role = stipulatorv1.BindingRole_BINDING_ROLE_PROVES

	fb := fakeBackend{"example.com/p.F": strings.Repeat("s", 64)}
	example := map[string]verify.Backend{"go": fakeClassifier{fb, verify.ExampleWitness}}
	if _, err := Bind(fsys, example, req); err == nil || !strings.Contains(err.Error(), "cannot discharge") {
		t.Fatalf("undischargeable proof accepted: %v", err)
	}

	proof := map[string]verify.Backend{"go": fakeClassifier{fb, verify.AnalyzerProof}}
	if _, err := Bind(fsys, proof, req); err != nil {
		t.Fatalf("analyzer test refused: %v", err)
	}

	// A backend with no classifier at all cannot discharge either.
	if _, err := Bind(fsys, backends, req); err == nil || !strings.Contains(err.Error(), "cannot discharge") {
		t.Fatalf("classifierless backend accepted a proof: %v", err)
	}

	// No loaded verifier: the claim cannot be checked at write time, so it
	// is refused rather than recorded.
	if _, err := Bind(fsys, map[string]verify.Backend{}, req); err == nil || !strings.Contains(err.Error(), "cannot be checked") {
		t.Fatalf("unloaded backend accepted a proof: %v", err)
	}
}
