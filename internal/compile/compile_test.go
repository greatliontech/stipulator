package compile

import (
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/proto"
)

// compileFiles compiles an in-memory corpus of specs/*.md files.
func compileFiles(t *testing.T, files map[string]string) (*stipulatorv1.Spec, []Diagnostic) {
	t.Helper()
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
	}
	for p, c := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(c)}
	}
	spec, diags, err := Compile(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return spec, diags
}

func wantDiag(t *testing.T, diags []Diagnostic, substr string) {
	t.Helper()
	for _, d := range diags {
		if strings.Contains(d.Message, substr) {
			return
		}
	}
	t.Fatalf("no diagnostic containing %q in %v", substr, diags)
}

func wantClean(t *testing.T, diags []Diagnostic) {
	t.Helper()
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
}

func req(t *testing.T, spec *stipulatorv1.Spec, id string) *stipulatorv1.Requirement {
	t.Helper()
	for _, r := range spec.GetRequirements() {
		if r.GetId() == id {
			return r
		}
	}
	t.Fatalf("requirement %s not in IR", id)
	return nil
}

func hasEdge(spec *stipulatorv1.Spec, kind stipulatorv1.EdgeKind, from, to *stipulatorv1.NodeRef) bool {
	for _, e := range spec.GetEdges() {
		if e.GetKind() == kind && proto.Equal(e.GetFrom(), from) && proto.Equal(e.GetTo(), to) {
			return true
		}
	}
	return false
}

func TestRequirementLead(t *testing.T) {
	spec, diags := compileFiles(t, map[string]string{
		"specs/a.md": "# T\n\n## S\n\n**REQ-a-one** (behavior): It MUST do the thing.\n\n" +
			"**REQ-a-two** (wire, refines REQ-a-one, depends REQ-a-one): It MUST hold:\n\n" +
			"- first point\n- second point\n\n| c1 | c2 |\n|---|---|\n| v1 | v2 |\n",
	})
	wantClean(t, diags)

	one := req(t, spec, "REQ-a-one")
	if one.GetKind() != stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR {
		t.Errorf("kind = %v", one.GetKind())
	}
	if one.GetKeyword() != stipulatorv1.Keyword_KEYWORD_MUST {
		t.Errorf("keyword = %v", one.GetKeyword())
	}
	if got := one.GetText(); got != "It MUST do the thing." {
		t.Errorf("text = %q (marker must be stripped)", got)
	}
	if !strings.HasPrefix(one.GetSource(), "**REQ-a-one**") {
		t.Errorf("source = %q (marker must be kept)", one.GetSource())
	}
	if len(one.GetContentHash()) != 64 {
		t.Errorf("content hash %q", one.GetContentHash())
	}

	two := req(t, spec, "REQ-a-two")
	for _, want := range []string{"first point", "second point", "v1", "v2"} {
		if !strings.Contains(two.GetText(), want) {
			t.Errorf("payload %q missing from text %q", want, two.GetText())
		}
	}
	if !strings.Contains(two.GetSource(), "| c1 | c2 |") {
		t.Errorf("payload missing from source %q", two.GetSource())
	}
	for _, kind := range []stipulatorv1.EdgeKind{
		stipulatorv1.EdgeKind_EDGE_KIND_REFINES,
		stipulatorv1.EdgeKind_EDGE_KIND_DEPENDS,
	} {
		if !hasEdge(spec, kind, reqRef("REQ-a-two"), reqRef("REQ-a-one")) {
			t.Errorf("missing %v edge", kind)
		}
	}
	if loc := two.GetLocation(); loc.GetDocument() != "specs/a.md" || len(loc.GetSectionPath()) != 1 || loc.GetSectionPath()[0] != "S" {
		t.Errorf("location = %v", loc)
	}
}

func TestPayloadChangesHash(t *testing.T) {
	base := "# T\n\n**REQ-x-a** (behavior): It MUST hold:\n\n- point\n"
	specA, diags := compileFiles(t, map[string]string{"specs/a.md": base})
	wantClean(t, diags)
	specB, diags := compileFiles(t, map[string]string{"specs/a.md": strings.Replace(base, "- point", "- other", 1)})
	wantClean(t, diags)
	if req(t, specA, "REQ-x-a").GetContentHash() == req(t, specB, "REQ-x-a").GetContentHash() {
		t.Fatal("payload edit did not change content hash")
	}
	// A paragraph after the requirement is NOT payload.
	specC, diags := compileFiles(t, map[string]string{
		"specs/a.md": "# T\n\n**REQ-x-a** (behavior): It MUST hold:\n\n- point\n\nTrailing prose paragraph.\n",
	})
	wantClean(t, diags)
	if strings.Contains(req(t, specC, "REQ-x-a").GetText(), "Trailing prose") {
		t.Fatal("paragraph leaked into payload")
	}
}

func TestHashStableUnderRewrap(t *testing.T) {
	a := "# T\n\n**REQ-w-a** (behavior): The system MUST do a thing that is long enough to wrap.\n"
	b := "# T\n\n**REQ-w-a** (behavior): The system MUST do\na thing that is long\nenough to wrap.\n"
	specA, diags := compileFiles(t, map[string]string{"specs/a.md": a})
	wantClean(t, diags)
	specB, diags := compileFiles(t, map[string]string{"specs/a.md": b})
	wantClean(t, diags)
	if req(t, specA, "REQ-w-a").GetContentHash() != req(t, specB, "REQ-w-a").GetContentHash() {
		t.Fatal("rewrapping changed the content hash")
	}
}

func TestLeadErrors(t *testing.T) {
	cases := []struct{ name, body, diag string }{
		{"near miss no metadata", "**REQ-x-a** does the thing.", "does not parse as a requirement lead"},
		{"unknown kind", "**REQ-x-a** (behaviour): It MUST x.", `unknown clause kind "behaviour"`},
		{"malformed clause", "**REQ-x-a** (behavior, refines): It MUST x.", "malformed metadata clause"},
		{"bad edge target", "**REQ-x-a** (behavior, refines REQ-Bad): It MUST x.", "does not match the identifier grammar"},
		{"unknown edge clause", "**REQ-x-a** (behavior, replaces REQ-x-b): It MUST x.", `unknown edge clause "replaces"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, diags := compileFiles(t, map[string]string{"specs/a.md": "# T\n\n" + c.body + "\n"})
			wantDiag(t, diags, c.diag)
		})
	}
}

func TestKeywordDiscipline(t *testing.T) {
	t.Run("zero keywords", func(t *testing.T) {
		_, diags := compileFiles(t, map[string]string{"specs/a.md": "# T\n\n**REQ-x-a** (behavior): It does the thing.\n"})
		wantDiag(t, diags, "has 0 normative keyword occurrences")
	})
	t.Run("two keywords", func(t *testing.T) {
		_, diags := compileFiles(t, map[string]string{"specs/a.md": "# T\n\n**REQ-x-a** (behavior): It MUST x and SHOULD y.\n"})
		wantDiag(t, diags, "has 2 normative keyword occurrences")
	})
	t.Run("code spans are inert", func(t *testing.T) {
		_, diags := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**REQ-x-a** (behavior): It MUST accept `MUST` and `MAY` mentions.\n",
		})
		wantClean(t, diags)
	})
	t.Run("keyword wrapped across a soft break", func(t *testing.T) {
		spec, diags := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**REQ-x-a** (behavior): It MUST\nNOT do the thing.\n",
		})
		wantClean(t, diags)
		if kw := req(t, spec, "REQ-x-a").GetKeyword(); kw != stipulatorv1.Keyword_KEYWORD_MUST_NOT {
			t.Fatalf("wrapped MUST NOT classified as %v", kw)
		}
	})
	t.Run("term name wrapped across a soft break", func(t *testing.T) {
		spec, diags := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**content hash** (term): a digest.\n\n" +
				"**REQ-x-a** (behavior): The content\nhash MUST be stable.\n",
		})
		wantClean(t, diags)
		if !hasEdge(spec, stipulatorv1.EdgeKind_EDGE_KIND_USES_TERM, reqRef("REQ-x-a"), termRef("content hash")) {
			t.Fatal("wrapped term name did not match")
		}
	})
	t.Run("MUST NOT is one occurrence", func(t *testing.T) {
		spec, diags := compileFiles(t, map[string]string{"specs/a.md": "# T\n\n**REQ-x-a** (behavior): It MUST NOT x.\n"})
		wantClean(t, diags)
		if req(t, spec, "REQ-x-a").GetKeyword() != stipulatorv1.Keyword_KEYWORD_MUST_NOT {
			t.Fatal("keyword not MUST NOT")
		}
	})
	t.Run("orphans", func(t *testing.T) {
		for name, body := range map[string]string{
			"annotation": "Plain prose where it MUST not be.",
			"heading":    "## The MUST section",
			"term":       "**thing** (term): a thing that MUST exist.",
			"note":       "> It MUST be noted.",
		} {
			t.Run(name, func(t *testing.T) {
				_, diags := compileFiles(t, map[string]string{"specs/a.md": "# T\n\n" + body + "\n"})
				wantDiag(t, diags, "outside requirement text")
			})
		}
	})
}

func TestIdentity(t *testing.T) {
	t.Run("duplicate requirement", func(t *testing.T) {
		_, diags := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**REQ-x-a** (behavior): It MUST x.\n",
			"specs/b.md": "# U\n\n**REQ-x-a** (behavior): It MUST y.\n",
		})
		wantDiag(t, diags, "duplicate requirement REQ-x-a")
	})
	t.Run("duplicate term case-insensitive", func(t *testing.T) {
		_, diags := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**Corpus** (term): the files.\n\n**corpus** (term): also the files.\n",
		})
		wantDiag(t, diags, "duplicate term")
	})
	t.Run("tombstoned identities rejected", func(t *testing.T) {
		fsys := fstest.MapFS{
			".stipulator/manifest.textproto":   {Data: []byte("include: \"specs/**/*.md\"\n")},
			".stipulator/tombstones.textproto": {Data: []byte("retired: \"REQ-x-old\"\nretired: \"Widget\"\n")},
			"specs/a.md":                       {Data: []byte("# T\n\n**REQ-x-old** (behavior): It MUST x.\n\n**widget** (term): a gadget.\n")},
		}
		_, diags, err := Compile(fsys)
		if err != nil {
			t.Fatal(err)
		}
		wantDiag(t, diags, "requirement REQ-x-old redeclares a tombstoned identity")
		wantDiag(t, diags, `term "widget" redeclares a tombstoned identity`)
	})
	t.Run("supersedes may target tombstone", func(t *testing.T) {
		fsys := fstest.MapFS{
			".stipulator/manifest.textproto":   {Data: []byte("include: \"specs/**/*.md\"\n")},
			".stipulator/tombstones.textproto": {Data: []byte("retired: \"REQ-x-old\"\n")},
			"specs/a.md":                       {Data: []byte("# T\n\n**REQ-x-new** (behavior, supersedes REQ-x-old): It MUST x.\n")},
		}
		_, diags, err := Compile(fsys)
		if err != nil {
			t.Fatal(err)
		}
		wantClean(t, diags)
	})
	t.Run("supersedes unknown fails", func(t *testing.T) {
		_, diags := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**REQ-x-new** (behavior, supersedes REQ-x-ghost): It MUST x.\n",
		})
		wantDiag(t, diags, "neither declared nor tombstoned")
	})
}

func TestReferences(t *testing.T) {
	t.Run("edge from requirement", func(t *testing.T) {
		spec, diags := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**REQ-x-a** (behavior): It MUST x.\n\n**REQ-x-b** (behavior): Like REQ-x-a, it MUST y.\n",
		})
		wantClean(t, diags)
		if !hasEdge(spec, stipulatorv1.EdgeKind_EDGE_KIND_REFERENCE, reqRef("REQ-x-b"), reqRef("REQ-x-a")) {
			t.Fatal("missing reference edge")
		}
	})
	t.Run("unresolved fails", func(t *testing.T) {
		_, diags := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**REQ-x-a** (behavior): Per REQ-x-ghost it MUST x.\n",
		})
		wantDiag(t, diags, "reference to REQ-x-ghost resolves to nothing")
	})
	t.Run("code spans inert for references", func(t *testing.T) {
		_, diags := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**REQ-x-a** (behavior): Like `REQ-x-ghost` it MUST x.\n",
		})
		wantClean(t, diags)
	})
	t.Run("annotation references are local", func(t *testing.T) {
		spec, diags := compileFiles(t, map[string]string{
			"specs/a.md": "# T\n\n**REQ-x-a** (behavior): It MUST x.\n\nSee REQ-x-a for details.\n",
		})
		wantClean(t, diags)
		anns := spec.GetAnnotations()
		if len(anns) != 1 || len(anns[0].GetReferences()) != 1 {
			t.Fatalf("annotations = %v", anns)
		}
		for _, e := range spec.GetEdges() {
			if e.GetKind() == stipulatorv1.EdgeKind_EDGE_KIND_REFERENCE && !e.GetFrom().HasRequirementId() && !e.GetFrom().HasTermName() {
				t.Fatal("identity-less edge in global edge list")
			}
		}
	})
}

func TestTermMatching(t *testing.T) {
	spec, diags := compileFiles(t, map[string]string{
		"specs/a.md": "# T\n\n**content hash** (term): a hash of content.\n\n**hash** (term): a digest.\n\n" +
			"**REQ-x-a** (behavior): The Content Hash MUST be stable.\n\n" +
			"**REQ-x-b** (behavior): The `hash` MUST be inert inside code spans.\n",
	})
	wantClean(t, diags)
	if !hasEdge(spec, stipulatorv1.EdgeKind_EDGE_KIND_USES_TERM, reqRef("REQ-x-a"), termRef("content hash")) {
		t.Fatal("missing uses-term edge for multi-word case-insensitive match")
	}
	if hasEdge(spec, stipulatorv1.EdgeKind_EDGE_KIND_USES_TERM, reqRef("REQ-x-a"), termRef("hash")) {
		t.Fatal("shorter term matched inside longer match")
	}
	if hasEdge(spec, stipulatorv1.EdgeKind_EDGE_KIND_USES_TERM, reqRef("REQ-x-b"), termRef("hash")) {
		t.Fatal("code span matched a term")
	}
	// Term definitions do not self-edge, but may use other terms.
	if hasEdge(spec, stipulatorv1.EdgeKind_EDGE_KIND_USES_TERM, termRef("content hash"), termRef("content hash")) {
		t.Fatal("self uses-term edge")
	}
	if !hasEdge(spec, stipulatorv1.EdgeKind_EDGE_KIND_USES_TERM, termRef("content hash"), termRef("hash")) {
		t.Fatal("term-to-term uses-term edge missing")
	}
}

func TestNotes(t *testing.T) {
	spec, diags := compileFiles(t, map[string]string{
		"specs/a.md": "# T\n\n**REQ-x-a** (behavior): It MUST x.\n\n> Attached commentary.\n\n## S\n\n> Section commentary.\n",
	})
	wantClean(t, diags)
	notes := spec.GetNotes()
	if len(notes) != 2 {
		t.Fatalf("notes = %d", len(notes))
	}
	// Note order is canonical by content, not document position, so
	// locate each by its text.
	byText := map[string]*stipulatorv1.Note{}
	for _, n := range notes {
		byText[n.GetText()] = n
	}
	if n := byText["Attached commentary."]; n.GetAttachedTo().GetRequirementId() != "REQ-x-a" {
		t.Errorf("attached note attachment = %v", n.GetAttachedTo())
	}
	if n := byText["Section commentary."]; n.HasAttachedTo() {
		t.Errorf("section note should have no attachment, got %v", n.GetAttachedTo())
	}
}

func TestDocumentErrors(t *testing.T) {
	t.Run("two titles", func(t *testing.T) {
		_, diags := compileFiles(t, map[string]string{"specs/a.md": "# T\n\n# U\n"})
		wantDiag(t, diags, "exactly one level-1 heading, found 2")
	})
	t.Run("invalid utf8", func(t *testing.T) {
		_, diags := compileFiles(t, map[string]string{"specs/a.md": "# T\n\n\xff\xfe\n"})
		wantDiag(t, diags, "not valid UTF-8")
	})
}

func TestLayoutIndependence(t *testing.T) {
	blocks := []string{
		"**REQ-l-a** (behavior): It MUST x.",
		"**REQ-l-b** (behavior, refines REQ-l-a): Using the widget it MUST y.",
		"**widget** (term): a gadget.",
	}
	one := map[string]string{
		"specs/a.md": "# T\n\n" + strings.Join(blocks, "\n\n") + "\n",
	}
	split := map[string]string{
		"specs/a.md": "# T\n\n" + blocks[0] + "\n",
		"specs/b.md": "# U\n\n## Deep\n\n" + blocks[1] + "\n\n" + blocks[2] + "\n",
	}
	specOne, diags := compileFiles(t, one)
	wantClean(t, diags)
	specSplit, diags := compileFiles(t, split)
	wantClean(t, diags)

	if !proto.Equal(stripLocations(specOne), stripLocations(specSplit)) {
		t.Fatalf("IRs differ beyond location metadata:\n%v\n---\n%v", stripLocations(specOne), stripLocations(specSplit))
	}
}

func TestGeneratedIndexExcluded(t *testing.T) {
	spec, diags := compileFiles(t, map[string]string{
		"specs/a.md":      "# T\n\n**REQ-x-a** (behavior): It MUST x.\n",
		"specs/README.md": "# Index\n\nProse that MUST not be compiled.\n",
	})
	wantClean(t, diags)
	if len(spec.GetDocuments()) != 1 {
		t.Fatalf("documents = %d", len(spec.GetDocuments()))
	}
}

// TestTermLint pins the opt-in lint: silent without the manifest opt-in
// (this repository's own corpus carries deliberate substring pairs),
// shadowing and denylist warnings when opted in — surfaced without
// failing compilation — and word-boundary matching (a plural is not a
// shadow).
func TestTermLint(t *testing.T) {
	stipulate.Covers(t, "REQ-profile-term-lint")
	doc := "# T\n\n**law node** (term): a node holding law.\n\n**node** (term): a graph vertex.\n\n**nodes overview** (term): prose about many.\n"

	compileWith := func(manifest string) (bool, []Diagnostic) {
		spec, diags, err := Compile(fstest.MapFS{
			".stipulator/manifest.textproto": {Data: []byte(manifest)},
			"specs/a.md":                     {Data: []byte(doc)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return spec != nil, diags
	}

	ok, diags := compileWith("include: \"specs/**/*.md\"\n")
	if !ok || len(diags) != 0 {
		t.Fatalf("lint fired without opt-in: %v", diags)
	}

	ok, diags = compileWith("include: \"specs/**/*.md\"\nterm_lint { warn_shadowing: true denylist: \"node\" }\n")
	if !ok {
		t.Fatal("warnings failed the compile")
	}
	var msgs []string
	for _, d := range diags {
		if !d.Warning {
			t.Fatalf("lint emitted an error-severity diagnostic: %v", d)
		}
		msgs = append(msgs, d.Message)
	}
	joined := strings.Join(msgs, "|")
	if !strings.Contains(joined, `term "law node" contains term "node"`) {
		t.Fatalf("shadowing not warned: %v", msgs)
	}
	if !strings.Contains(joined, `term "node" matches the manifest denylist`) {
		t.Fatalf("denylist not warned: %v", msgs)
	}
	// "nodes overview" contains "node" only mid-word: no warning.
	if strings.Contains(joined, "nodes overview") {
		t.Fatalf("plural mid-word shadow warned: %v", msgs)
	}
}
