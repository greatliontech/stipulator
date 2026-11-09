package check

import (
	"context"
	"os"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/trace"
	"github.com/greatliontech/stipulator/internal/verify"
)

// LadderDeps wires the backend construction and optional witness execution
// for one shared verification ladder pass.
type LadderDeps struct {
	Backends func(ctx context.Context, symbols []string) (map[string]verify.Backend, error)
	RunTests func(ctx context.Context, seeding verify.WitnessSeeding) (*verify.TestRun, error)
}

// Ladder runs the shared resolution-witness-correlation ladder over a
// prepared corpus and optional policy capture:
//  1. derives the operation's symbol set from store and pc,
//  2. opens the serving backends and installs their publishing closer,
//  3. resolves bindings in PHASE_DISCOVERY ahead of any execution
//     (REQ-evidence-resolution-freshness-quiesce),
//  4. when RunTests is non-nil, executes witnesses with verify.SeedingOf(backends),
//     attaching closer() notices on any error
//     (REQ-evidence-resolution-freshness-account),
//  5. correlates the resolved report with the witness run in PHASE_VERIFICATION
//     and records closer() on report.ResolutionNotices.
func Ladder(ctx context.Context, spec *stipulatorv1.Spec, store *records.Store, pc *golang.Capture, witnessed bool, d LadderDeps) (*verify.Report, *verify.TestRun, error) {
	trace.Refresh(os.Getenv(trace.EnvTrace), os.Getenv(trace.EnvTraceFile))
	rep := progress.FromContext(ctx)
	rep.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	symStart := time.Now()
	symbols, err := golang.OperationSymbols(ctx, store, pc)
	trace.Emit(ctx, trace.DomainDiscover, "operation_symbols", "corpus",
		trace.Int("symbols", len(symbols)),
		trace.Int64("duration_ms", time.Since(symStart).Milliseconds()),
		trace.Err(err),
	)
	if err != nil {
		return nil, nil, err
	}
	bStart := time.Now()
	backends, err := d.Backends(ctx, symbols)
	trace.Emit(ctx, trace.DomainResolve, "backends_open", "corpus",
		trace.Int("symbols", len(symbols)),
		trace.Int64("duration_ms", time.Since(bStart).Milliseconds()),
		trace.Err(err),
	)
	if err != nil {
		return nil, nil, err
	}
	closer := verify.Closer(backends)
	defer closer()
	resStart := time.Now()
	report := verify.Resolve(spec, store, backends, witnessed)
	trace.Emit(ctx, trace.DomainResolve, "verify_resolve", "corpus",
		trace.Int("results", len(report.Results)),
		trace.Int("problems", len(report.Problems)),
		trace.Int64("duration_ms", time.Since(resStart).Milliseconds()),
	)
	var tr *verify.TestRun
	if d.RunTests != nil {
		if tr, err = d.RunTests(ctx, verify.SeedingOf(backends)); err != nil {
			return nil, nil, verify.WithNotices(err, closer())
		}
	}
	rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	corrStart := time.Now()
	report = verify.Correlate(report, store, tr)
	report.ResolutionNotices = closer()
	trace.Emit(ctx, trace.DomainVerify, "verify_correlate", "corpus",
		trace.Int("notices", len(report.ResolutionNotices)),
		trace.Int64("duration_ms", time.Since(corrStart).Milliseconds()),
	)
	return report, tr, nil
}
