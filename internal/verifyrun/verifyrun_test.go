package verifyrun

import (
	"context"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verbcore"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// A face's dependencies that refuse every leg past the preparation.
func untouchable(t *testing.T, prepared *check.Prepared) verbcore.Deps {
	t.Helper()
	return verbcore.Deps{
		Prepare: func() (*check.Prepared, error) { return prepared, nil },
		Capture: func(context.Context) (*golang.Capture, error) {
			t.Fatal("the policy was captured")
			return nil, nil
		},
		Backends: func(context.Context, []string) (map[string]verify.Backend, error) {
			t.Fatal("backends were built")
			return nil, nil
		},
		RunTests: func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool, string) (*verify.TestRun, error) {
			t.Fatal("a witness run started")
			return nil, nil
		},
	}
}

// An unknown requirement identifier refuses against the freshly
// compiled corpus before any child process — never an empty result,
// never one that costs the pass to hear (REQ-check-preparation).
func TestRunRefusesUnknownIDsBeforeAnyChild(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: &records.Store{}}
	_, _, _, err := Run(context.Background(), untouchable(t, prepared), false, []string{"REQ-ghost"})
	if err == nil || !strings.Contains(err.Error(), "REQ-ghost") {
		t.Fatalf("unknown id = %v, want a refusal naming it", err)
	}
}

// Records that fail hygiene take the record-only form: the report
// carries the problems, no policy is captured, no backend built, no
// witness run — and the run is nil so a coverage caller knows the pass
// never witnessed (REQ-check-preparation).
func TestRunTakesTheRecordOnlyFormUnderHygieneProblems(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: &records.Store{}, Hygiene: []verify.Problem{{Path: "a", Message: "stale"}}}
	p, rep, tr, err := Run(context.Background(), untouchable(t, prepared), false, nil)
	if err != nil || p != prepared || tr != nil || rep == nil {
		t.Fatalf("record-only form = %v %v %v %v", p, rep, tr, err)
	}
	if rep.Witnessed {
		t.Fatal("the record-only report claims a witness run")
	}
}

// The no-test form resolves without a policy record: nothing is
// captured and no witness runs; the backends still resolve the
// bindings (REQ-evidence-resolution-freshness).
func TestRunNoTestCapturesNoPolicy(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: &records.Store{}}
	deps := untouchable(t, prepared)
	built := false
	deps.Backends = func(context.Context, []string) (map[string]verify.Backend, error) {
		built = true
		return map[string]verify.Backend{}, nil
	}
	_, rep, tr, err := Run(context.Background(), deps, true, nil)
	if err != nil || tr != nil || !built || rep == nil || rep.Witnessed {
		t.Fatalf("no-test form = %v %v built=%v err=%v", rep, tr, built, err)
	}
}

// One capture of the accepted policy serves the run and the served set
// alike: the witnessed form captures exactly once, and the run receives
// that capture (REQ-check-derivation).
func TestRunCapturesThePolicyOnce(t *testing.T) {
	stipulate.Covers(t, "REQ-check-derivation")
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: &records.Store{}}
	deps := untouchable(t, prepared)
	captures := 0
	captured := &golang.Capture{}
	deps.Capture = func(context.Context) (*golang.Capture, error) {
		captures++
		return captured, nil
	}
	deps.Backends = func(context.Context, []string) (map[string]verify.Backend, error) {
		return map[string]verify.Backend{}, nil
	}
	var ran *golang.Capture
	deps.RunTests = func(_ context.Context, pc *golang.Capture, _ verify.WitnessSeeding, scope map[gofresh.Subject]bool, _ string) (*verify.TestRun, error) {
		ran = pc
		if scope != nil {
			t.Fatal("the verification pass scoped its run")
		}
		return &verify.TestRun{SelectiveServing: true}, nil
	}
	_, rep, tr, err := Run(context.Background(), deps, false, nil)
	if err != nil || tr == nil || !rep.Witnessed {
		t.Fatalf("witnessed form = %v %v %v", rep, tr, err)
	}
	if captures != 1 || ran != captured {
		t.Fatalf("captures = %d, run received the capture = %v; want one capture reaching the run", captures, ran == captured)
	}
}

// The scoped pass captures the policy and runs exactly when the scope
// holds a subject: a records-only judgment (nil scope) and a scope no
// bound witness can move (empty) capture nothing, run nothing, and
// report with a nil run; a populated scope captures once and hands
// the run that scope (REQ-gap-resolved-pruned, REQ-gap-list).
func TestScopedCapturesOnlyWhenAsked(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned", "REQ-gap-list")
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: &records.Store{}}
	deps := untouchable(t, prepared)
	deps.Backends = func(context.Context, []string) (map[string]verify.Backend, error) {
		return map[string]verify.Backend{}, nil
	}
	for _, scope := range []map[gofresh.Subject]bool{nil, {}} {
		rep, tr, err := Scoped(context.Background(), deps, prepared, scope, "")
		if err != nil || tr != nil || rep == nil || rep.Witnessed {
			t.Fatalf("scope %v: scoped pass = %v %v %v", scope, rep, tr, err)
		}
	}
	scope := map[gofresh.Subject]bool{{Package: "example.com/p", Symbol: "TestA"}: true}
	captured := &golang.Capture{}
	captures := 0
	deps.Capture = func(context.Context) (*golang.Capture, error) { captures++; return captured, nil }
	var got map[gofresh.Subject]bool
	deps.RunTests = func(_ context.Context, pc *golang.Capture, _ verify.WitnessSeeding, s map[gofresh.Subject]bool, why string) (*verify.TestRun, error) {
		if pc != captured || why != "why" {
			t.Fatalf("run received pc=%v why=%q", pc == captured, why)
		}
		got = s
		return &verify.TestRun{SelectiveServing: true}, nil
	}
	rep, tr, err := Scoped(context.Background(), deps, prepared, scope, "why")
	if err != nil || tr == nil || !rep.Witnessed || captures != 1 || len(got) != 1 || !got[gofresh.Subject{Package: "example.com/p", Symbol: "TestA"}] {
		t.Fatalf("witnessed scoped pass = %v %v %v captures=%d scope=%v", rep, tr, err, captures, got)
	}
}

// A store without gap records skips the witness evidence, never the
// compilation: the coverage is nil and no capture, backend, or run
// happens (REQ-gap-list).
func TestGapsGathersNothingWithoutGapRecords(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-list")
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: &records.Store{}}
	p, rep, cov, err := Gaps(context.Background(), untouchable(t, prepared))
	if err != nil || p != prepared || rep != nil || cov != nil {
		t.Fatalf("gapless list = %v %v %v %v", p, rep, cov, err)
	}
}

// A gap whose requirement binds no witness yields an empty scope: no
// bound witness can move a gap-relevant bucket, so no witness evidence
// is taken — no capture, no run — while the report and the coverage
// still evaluate over the records (REQ-gap-list).
func TestGapsCapturesNothingWithoutAScope(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-list")
	id := "REQ-x"
	store := &records.Store{Gaps: []records.GapFile{{Path: ".stipulator/gaps/x.textproto", Gap: stipulatorv1.Gap_builder{RequirementId: &id}.Build()}}}
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: store}
	deps := untouchable(t, prepared)
	deps.Backends = func(context.Context, []string) (map[string]verify.Backend, error) {
		return map[string]verify.Backend{}, nil
	}
	_, rep, cov, err := Gaps(context.Background(), deps)
	if err != nil || rep == nil || cov == nil || rep.Witnessed {
		t.Fatalf("unbound gap list = %v %v %v", rep, cov, err)
	}
}
