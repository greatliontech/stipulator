package check

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestKnownIDsRefusesUnknownIdentifiers pins the vocabulary refusal
// every id-scoped surface shares: identifiers the corpus does not
// declare refuse by name; declared ones pass.
func TestKnownIDsRefusesUnknownIdentifiers(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	prepared, err := Prepare(fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte("# A\n\n**REQ-a-one** (behavior): It MUST hold.\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := KnownIDs(prepared.Spec, []string{"REQ-a-one"}); err != nil {
		t.Fatalf("declared identifier refused: %v", err)
	}
	err = KnownIDs(prepared.Spec, []string{"REQ-a-one", "REQ-a-two", "REQ-b"})
	if err == nil || !strings.Contains(err.Error(), "REQ-a-two, REQ-b") {
		t.Fatalf("err = %v, want every unknown identifier named", err)
	}
	if len(prepared.Hygiene) != 0 || prepared.Coverage == nil {
		t.Fatalf("prepared = %+v, want clean hygiene and a coverage policy", prepared)
	}
}

// The check result's compile problems carry the compile's remedies as
// their own rows beside the faults — marked as remedies, never counted
// as faults — so an agent reading a broken-corpus verdict sees the
// operation that renders the state (REQ-change-remediation).
//
//gofresh:pure
func TestCompileProblemsCarryRemedies(t *testing.T) {
	stipulate.Covers(t, "REQ-change-remediation")
	prepared, err := Prepare(fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte("# A\n\n**REQ-a-new** (behavior, supersedes REQ-a-gone): It MUST hold.\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	problems := prepared.CompileProblems()
	var fault, remedy bool
	for _, p := range problems {
		switch {
		case strings.HasPrefix(p.GetMessage(), "remedy: if REQ-a-gone was removed by this edit") && strings.Contains(p.GetMessage(), "stipulator dispose supersede --from REQ-a-gone --into REQ-a-new"):
			remedy = true
		case strings.Contains(p.GetMessage(), "supersedes REQ-a-gone, which is neither declared nor tombstoned"):
			fault = true
		}
	}
	if !fault || !remedy || len(problems) != 2 {
		t.Fatalf("compile problems = %v, want the fault and its remedy row", problems)
	}
	// A compiling corpus yields no problems at all — a remedy never
	// stands alone.
	clean, err := Prepare(fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte("# A\n\n**REQ-a-one** (behavior): It MUST hold.\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(clean.CompileProblems()) != 0 {
		t.Fatal("a compiling corpus reported problems")
	}
}
