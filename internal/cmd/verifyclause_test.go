package cmd

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// The CLI verify row's clause column is the one clause head with a
// leading space, empty for a whole-requirement claim.
func TestVerifyRowClauseColumnIsTheClauseHead(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-clause-claim")
	if got := clauseColumn(verify.BindingResult{}); got != "" {
		t.Fatalf("whole-requirement column = %q", got)
	}
	c := &stipulatorv1.Clause{}
	c.SetOrdinal(3)
	c.SetLabel("alpha")
	if got := clauseColumn(verify.BindingResult{Clause: c}); got != " clause 3 `alpha`" {
		t.Fatalf("clause column = %q", got)
	}
}
