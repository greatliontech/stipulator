package views

import (
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestViewVocabulariesAreOneSource pins the validators to the renderers:
// every word a validator accepts renders, every word it refuses the
// renderer refuses with the same message, so a surface validating at
// parse can never disagree with the view it later renders.
func TestViewVocabulariesAreOneSource(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	cov := &coverage.Report{}
	vr := &verify.Report{}
	for _, word := range append([]string{""}, coverageViews...) {
		if err := ValidateCoverageView(word); err != nil {
			t.Fatalf("coverage view %q refused by the validator: %v", word, err)
		}
		if _, err := CoverageView(cov, Facts{}, word, Scope{}); err != nil {
			t.Fatalf("coverage view %q refused by the renderer: %v", word, err)
		}
	}
	for _, word := range append([]string{""}, verifyViews...) {
		if err := ValidateVerifyView(word); err != nil {
			t.Fatalf("verify view %q refused by the validator: %v", word, err)
		}
		if _, err := VerifyView(vr, Facts{}, word, Scope{}); err != nil {
			t.Fatalf("verify view %q refused by the renderer: %v", word, err)
		}
	}
	verr := ValidateCoverageView("bogus")
	_, rerr := CoverageView(cov, Facts{}, "bogus", Scope{})
	if verr == nil || rerr == nil || verr.Error() != rerr.Error() || !strings.Contains(verr.Error(), "summary, reds, full") {
		t.Fatalf("coverage refusals differ: validator %v, renderer %v", verr, rerr)
	}
	verr = ValidateVerifyView("bogus")
	_, rerr = VerifyView(vr, Facts{}, "bogus", Scope{})
	if verr == nil || rerr == nil || verr.Error() != rerr.Error() || !strings.Contains(verr.Error(), "summary, bindings") {
		t.Fatalf("verify refusals differ: validator %v, renderer %v", verr, rerr)
	}
}
