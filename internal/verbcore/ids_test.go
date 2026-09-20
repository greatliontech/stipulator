package verbcore

import (
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// The identifier-list grammar is one on both faces: every occurrence's
// comma-separated identifiers join with blanks dropped (an occurrence
// silently dropped would be the accept-and-drop REQ-evidence-claim-batch
// forbids), a JSON-array encoding is tolerated, and a list given but
// reducing to nothing refuses — never the whole corpus
// (REQ-check-preparation).
//
//gofresh:pure
func TestSplitIDsIsTheOneGrammar(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-claim-batch")
	got, err := SplitIDLists([]string{"REQ-a, REQ-b", "REQ-c", ""})
	if err != nil || !slices.Equal(got, []string{"REQ-a", "REQ-b", "REQ-c"}) {
		t.Fatalf("SplitIDLists = %v, %v", got, err)
	}
	if got, err := SplitIDLists(nil); got != nil || err != nil {
		t.Fatalf("no value given = %v, %v; want no selection", got, err)
	}
	for _, blank := range []string{",", " , ", "", "[]", `["", " "]`} {
		if _, err := SplitIDs(blank); err == nil || !strings.Contains(err.Error(), "no requirement identifiers given") {
			t.Fatalf("SplitIDs(%q) = %v; want the empty-reduction refusal", blank, err)
		}
	}
	for _, given := range [][]string{{","}, {""}, {" ", ","}} {
		if _, err := SplitIDLists(given); err == nil || !strings.Contains(err.Error(), "no requirement identifiers given") {
			t.Fatalf("a repetition %q reducing to nothing = %v; want the refusal — a value given is a selection asked for", given, err)
		}
	}
	if got, err := SplitIDs(`["REQ-a", "REQ-b"]`); err != nil || !slices.Equal(got, []string{"REQ-a", "REQ-b"}) {
		t.Fatalf("JSON array = %v, %v", got, err)
	}
	if _, err := SplitIDs("[not json"); err == nil {
		t.Fatal("a malformed JSON array parsed")
	}
	if got, err := SplitIDsLoose("  "); got != nil || err != nil {
		t.Fatalf("loose blank = %v, %v; want no selection", got, err)
	}
	if _, err := SplitIDsLoose(","); err == nil {
		t.Fatal("loose comma-only list parsed as no selection; want the refusal")
	}
}
