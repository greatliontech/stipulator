package verifyrun

import (
	"errors"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// RefuseProblems is the one hygiene refusal both faces render: nil for a
// clean record, the typed error carrying every problem otherwise
// (REQ-check-preparation).
//
//gofresh:pure
func TestRefuseProblemsIsTheOneRefusal(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	if err := RefuseProblems(nil); err != nil {
		t.Fatalf("clean record refused: %v", err)
	}
	problems := []verify.Problem{{Path: "bindings/a.textproto", Message: "dangling"}, {Path: "gaps/b.textproto", Message: "duplicate"}}
	err := RefuseProblems(problems)
	var pe *ProblemsError
	if !errors.As(err, &pe) || len(pe.Problems) != 2 || err.Error() != "verification problems (2); fix them first" {
		t.Fatalf("RefuseProblems = %v (%T)", err, err)
	}
}
