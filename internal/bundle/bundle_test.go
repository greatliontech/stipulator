package bundle

import (
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/stipulate"
)

const doc = "# T\n\n" +
	"**widget** (term): a gadget using a sprocket.\n\n" +
	"**sprocket** (term): a toothed part.\n\n" +
	"**REQ-b-a** (behavior): Using the widget it MUST x, per REQ-b-b.\n\n" +
	"**REQ-b-b** (behavior, refines REQ-b-c): It MUST y.\n\n" +
	"**REQ-b-c** (behavior): It MUST z.\n\n" +
	"**REQ-b-lone** (behavior): Alone it MUST w.\n\n" +
	"**REQ-b-old** (behavior, supersedes REQ-b-lone): Replacing, it MUST v.\n\n" +
	"## Deep\n\n**REQ-b-deep** (behavior): Deeply it MUST u.\n\n> See REQ-b-far for background.\n\n**REQ-b-far** (behavior): Far it MUST t.\n"

func compileDoc(t *testing.T) *stipulatorv1.Spec {
	t.Helper()
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(doc)},
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	return spec
}

func ids(b *stipulatorv1.Spec) []string {
	var out []string
	for _, r := range b.GetRequirements() {
		out = append(out, r.GetId())
	}
	return out
}

//gofresh:pure
func TestClosure(t *testing.T) {
	stipulate.Covers(t, "REQ-model-closure")
	spec := compileDoc(t)

	b, err := Compute(spec, []string{"REQ-b-a"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(ids(b), " ")
	// Transitive: a references b, b refines c. lone and old are outside.
	for _, want := range []string{"REQ-b-a", "REQ-b-b", "REQ-b-c"} {
		if !strings.Contains(got, want) {
			t.Errorf("closure missing %s: %s", want, got)
		}
	}
	for _, not := range []string{"REQ-b-lone", "REQ-b-old"} {
		if strings.Contains(got, not) {
			t.Errorf("closure leaked %s: %s", not, got)
		}
	}
	// Terms: widget used by a; widget's own text uses sprocket.
	var terms []string
	for _, tm := range b.GetTerms() {
		terms = append(terms, tm.GetName())
	}
	ts := strings.Join(terms, " ")
	if !strings.Contains(ts, "widget") || !strings.Contains(ts, "sprocket") {
		t.Fatalf("term closure incomplete: %s", ts)
	}

	// Supersedes is lifecycle, not context: old does not pull lone.
	b2, err := Compute(spec, []string{"REQ-b-old"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(ids(b2), " "), "REQ-b-lone") {
		t.Fatal("supersedes traversed into closure")
	}

	if _, err := Compute(spec, []string{"REQ-b-ghost"}); err == nil {
		t.Fatal("unknown identifier accepted")
	}
}

// TestBundleSelfContained pins the bundle contract: every requirement
// identifier and term name occurring in the rendered bundle resolves
// within it.
//
//gofresh:pure
func TestBundleSelfContained(t *testing.T) {
	stipulate.Covers(t, "REQ-model-bundle")
	spec := compileDoc(t)
	b, err := Compute(spec, []string{"REQ-b-a"})
	if err != nil {
		t.Fatal(err)
	}
	md := Markdown(b, []string{"REQ-b-a"})

	inBundle := map[string]bool{}
	for _, r := range b.GetRequirements() {
		inBundle[r.GetId()] = true
	}
	for _, m := range regexp.MustCompile(`\bREQ(-[a-z0-9]+)+\b`).FindAllString(md, -1) {
		if !inBundle[m] {
			t.Errorf("bundle mentions %s but does not contain it", m)
		}
	}
	for _, tm := range b.GetTerms() {
		if !strings.Contains(md, tm.GetSource()) {
			t.Errorf("term %q definition missing from rendering", tm.GetName())
		}
	}
	if !strings.Contains(md, "content_hash:") {
		t.Fatal("rendering carries no version anchors")
	}
	if !strings.Contains(md, "## Requested requirements") {
		t.Fatal("requested section missing")
	}
}

// TestBundleFixpointPullsNoteReferences pins the fixed point: a note in an
// included section referencing an out-of-closure requirement pulls that
// requirement in, so nothing rendered dangles.
//
//gofresh:pure
func TestBundleFixpointPullsNoteReferences(t *testing.T) {
	spec := compileDoc(t)
	b, err := Compute(spec, []string{"REQ-b-deep"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(ids(b), " ")
	if !strings.Contains(got, "REQ-b-far") {
		t.Fatalf("note reference not pulled into closure: %s", got)
	}
	md := Markdown(b, []string{"REQ-b-deep"})
	inBundle := map[string]bool{}
	for _, r := range b.GetRequirements() {
		inBundle[r.GetId()] = true
	}
	for _, m := range regexp.MustCompile(`\bREQ(-[a-z0-9]+)+\b`).FindAllString(md, -1) {
		if !inBundle[m] {
			t.Errorf("bundle mentions %s but does not contain it", m)
		}
	}
}
