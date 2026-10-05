package verifyrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/check"
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

// The report carries the serving path's account, read after the one
// close that publishes — on the whole-tree pass and the scoped pass
// alike — so a publish refused or degraded is never a fault nobody sees
// (REQ-evidence-resolution-freshness).
func TestPassesCarryTheServingAccountReadAfterTheClose(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
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
