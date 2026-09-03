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
