package compile

import (
	"reflect"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// A requirement's enforcement pointers are the code spans of its
// "Enforced by" sentence — a comma-and-"and" list with a qualifying
// phrase reads as one sentence, the sentence ends at the next period,
// a code span that is not an identifier is no pointer, and a
// requirement without the phrase names none; the phrase inside a
// sentence is prose (REQ-change-enforcement-pointers).
//
//gofresh:pure
func TestEnforcementPointersAreTheSentencesCodeSpans(t *testing.T) {
	stipulate.Covers(t, "REQ-change-enforcement-pointers")
	spec, diags := compileFiles(t, map[string]string{
		"specs/a.md": "# T\n\n**REQ-p-a** (behavior): It MUST x. Enforced by `TestA`, `pkg.Qualified`, `TestB`, and the CLI arm of `TestC`. Later text names `NotAPointer`.\n\n**REQ-p-b** (behavior): It MUST y; see `TestD`, which is Enforced by `TestG` in prose.\n\n**REQ-p-c** (behavior): It MUST z. Enforced by `TestE` and\n`TestF`.\n",
	})
	wantClean(t, diags)
	got := map[string][]string{}
	for _, r := range spec.GetRequirements() {
		var names []string
		for _, p := range r.GetEnforcementPointers() {
			names = append(names, p.GetName())
		}
		got[r.GetId()] = names
	}
	want := map[string][]string{"REQ-p-a": {"TestA", "TestB", "TestC"}, "REQ-p-b": nil, "REQ-p-c": {"TestE", "TestF"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pointers = %v, want %v", got, want)
	}
	// Every pointer's span addresses its own spelling in the
	// requirement's raw source — the one grammar answers "where" as
	// well as "what".
	for _, r := range spec.GetRequirements() {
		src := r.GetSource()
		for _, p := range r.GetEnforcementPointers() {
			if s, e := int(p.GetStart()), int(p.GetEnd()); s < 0 || e > len(src) || src[s:e] != p.GetName() {
				t.Fatalf("%s: pointer %q spans %d:%d = %q in %q", r.GetId(), p.GetName(), s, e, src[max(0, min(s, len(src))):min(max(e, 0), len(src))], src)
			}
		}
	}
}
