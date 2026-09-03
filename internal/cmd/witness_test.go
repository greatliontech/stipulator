package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestWitnessRecordProblemsCarryTheRecordPath pins the witness command's
// rendering of a record problem, found by the loader or by the run's
// discovery: the record's path leads, the class survives, and any other
// fault passes unchanged.
func TestWitnessRecordProblemsCarryTheRecordPath(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	record := policy.RecordError(errors.New("invocation \"all\": \"./vanished\" matched no packages"))
	got := withRecordPath(record)
	if !errors.Is(got, policy.ErrRecord) || !strings.HasPrefix(got.Error(), policy.Path+": ") || !strings.Contains(got.Error(), "./vanished") {
		t.Fatalf("record problem rendered as %v", got)
	}
	operational := errors.New("open x: permission denied")
	if got := withRecordPath(operational); got != operational {
		t.Fatalf("operational fault reshaped: %v", got)
	}
}
