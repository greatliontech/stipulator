package verifyrun

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// accountBackend is a backend whose account changes at its close — the
// close publishes, so the publish line exists only after it — and that
// counts its closes.
type accountBackend struct {
	closes int
}

func (b *accountBackend) Resolve(string) (verify.Resolution, string, error) {
	return verify.Resolved, strings.Repeat("a", 64), nil
}

func (b *accountBackend) Close() error {
	b.closes++
	return nil
}

func (b *accountBackend) Notices() []string {
	out := []string{"resolution: 0 served from records, 1 resolved typed", "resolution typed: example.com/p.F: no record"}
	if b.closes > 0 {
		out = append(out, "resolution published under \"default\": 1 record")
	}
	return out
}

// The pass emits the phases it owns — compile, then discovery for the
// bindings' resolution, then verification for the correlation — and no
// execution mark of its own: the witness run announces its phases, so
// the first execution-phase reading is the run's, with the pass's
// backends already released (REQ-evidence-resolution-freshness-quiesce).
func TestPassEmitsThePhasesItOwns(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-quiesce")
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: &records.Store{}}
	var phases []stipulatorv1.Phase
	rep := progress.New(func(e *stipulatorv1.ProgressEvent) {
		if p := e.GetPhase(); len(phases) == 0 || phases[len(phases)-1] != p {
			phases = append(phases, p)
		}
	}, progress.WithInterval(time.Hour))
	ctx := progress.NewContext(context.Background(), rep)
	deps := untouchable(t, prepared)
	deps.Backends = func(context.Context, []string) (map[string]verify.Backend, error) {
		return map[string]verify.Backend{"go": &accountBackend{}}, nil
	}
	deps.Capture = func(context.Context) (*golang.Capture, error) { return nil, nil }
	deps.RunTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool, string) (*verify.TestRun, error) {
		return &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}}, nil
	}
	if _, _, _, err := Run(ctx, deps, false, nil); err != nil {
		t.Fatal(err)
	}
	want := []stipulatorv1.Phase{stipulatorv1.Phase_PHASE_COMPILE, stipulatorv1.Phase_PHASE_DISCOVERY, stipulatorv1.Phase_PHASE_VERIFICATION}
	if !slices.Equal(phases, want) {
		t.Fatalf("the pass emitted %v; want %v — its own phases, no execution mark", phases, want)
	}
}

// The report carries the serving path's account, read after the one
// close that publishes — on the whole-tree pass and the scoped pass
// alike — so a publish refused or degraded is never a fault nobody sees
// (REQ-evidence-resolution-freshness-account).
func TestPassesCarryTheServingAccountReadAfterTheClose(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-account")
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: &records.Store{}}
	for _, form := range []string{"run", "scoped"} {
		b := &accountBackend{}
		deps := untouchable(t, prepared)
		deps.Backends = func(context.Context, []string) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": b}, nil
		}
		var rep *verify.Report
		var err error
		if form == "run" {
			_, rep, _, err = Run(context.Background(), deps, true, nil)
		} else {
			rep, _, err = Scoped(context.Background(), deps, prepared, nil, "")
		}
		if err != nil {
			t.Fatalf("%s: %v", form, err)
		}
		if b.closes != 1 {
			t.Fatalf("%s: the backend closed %d times; want once", form, b.closes)
		}
		joined := strings.Join(rep.ResolutionNotices, "\n")
		if !strings.Contains(joined, "resolution published under \"default\": 1 record") || !strings.Contains(joined, "resolution typed: example.com/p.F") {
			t.Fatalf("%s: report account = %q; want the account read after the close, the publish line included", form, joined)
		}
		if !strings.Contains(strings.Join(rep.Proto().GetResolutionNotices(), "\n"), "resolution published under") {
			t.Fatalf("%s: the wire report dropped the account", form)
		}
		// A refusal raised after the publishing close carries the same
		// account.
		rep.Problems = []verify.Problem{{Path: "a", Message: "broken"}}
		var pe *ProblemsError
		if err := RefuseReport(rep); !errors.As(err, &pe) || len(pe.Notices) != len(rep.ResolutionNotices) {
			t.Fatalf("%s: the refusal carries %v; want the report's account", form, err)
		}
	}
	if RefuseReport(&verify.Report{}) != nil {
		t.Fatal("a clean report refused")
	}
}
