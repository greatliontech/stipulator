package records

import (
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// A clause's name is one spelling: the head alone, and the heading is
// the head followed by the text prefix.
func TestClauseHeadIsTheHeadingsHead(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-clause-claim")
	c := &stipulatorv1.Clause{}
	c.SetOrdinal(3)
	c.SetText("The third clause.")
	if got := ClauseHead(c); got != "clause 3" {
		t.Fatalf("head = %q", got)
	}
	c.SetLabel("alpha")
	if got := ClauseHead(c); got != "clause 3 `alpha`" {
		t.Fatalf("labelled head = %q", got)
	}
	if got := ClauseHeading(c); !strings.HasPrefix(got, "clause 3 `alpha` (") {
		t.Fatalf("heading does not lead with the head: %q", got)
	}
}
