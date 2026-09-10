// Package verifyrun is the verification pass every verb that judges a
// tree runs first — verify, gate, context, partitions — computed once
// for both faces: the prepared corpus, the report, and the witness run
// the report was correlated with.
package verifyrun

import (
	"context"
	"fmt"

	"github.com/greatliontech/gofresh"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/verbcore"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Run is the verification pass. The exact-id scope validates against
// the freshly compiled corpus and BEFORE the witness run — a typo is a
// refusal, never an empty result, and never one that costs the
// expensive pass to hear (REQ-mcp-response-contract); records that fail
// hygiene fail verification whatever a witness run or a resolution
// would say, so the pass takes its record-only form with no child
// process (REQ-check-preparation); one capture of the accepted policy
// serves the run and the served set alike (REQ-check-derivation), and
// only a witness run consumes it, so the no-test form resolves without
// a record. The returned run is nil on the no-test form and on the
// record-only form, so a caller evaluating coverage knows whether the
// pass witnessed.
func Run(ctx context.Context, d verbcore.Deps, noTest bool, ids []string) (*check.Prepared, *verify.Report, *verify.TestRun, error) {
	rep := progress.FromContext(ctx)
	rep.Phase(stipulatorv1.Phase_PHASE_COMPILE)
	prepared, err := d.Prepare()
	if err != nil {
		return nil, nil, nil, err
	}
	spec, store := prepared.Spec, prepared.Store
	if len(ids) > 0 {
		if err := check.KnownIDs(spec, ids); err != nil {
			return nil, nil, nil, err
		}
	}
	if len(prepared.Hygiene) > 0 {
		rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
		return prepared, verify.Run(spec, store, nil, nil), nil, nil
	}
	var pc *golang.Capture
	if !noTest {
		if pc, err = d.Capture(ctx); err != nil {
			return nil, nil, nil, err
		}
	}
	symbols, err := golang.OperationSymbols(ctx, store, pc)
	if err != nil {
		return nil, nil, nil, err
	}
	backends, err := d.Backends(ctx, symbols)
	if err != nil {
		return nil, nil, nil, err
	}
	defer verify.CloseBackends(backends)
	var tr *verify.TestRun
	if !noTest {
		rep.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
		if tr, err = d.RunTests(ctx, pc, verify.SeedingOf(backends), nil, ""); err != nil {
			return nil, nil, nil, err
		}
	}
	rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	return prepared, verify.Run(spec, store, backends, tr), tr, nil
}

// Scoped is the verification pass narrowed to a subject scope over an
// already prepared corpus: the operation's symbols, the backends, the
// report — and, exactly when the scope holds a subject, the accepted
// policy captured and the scoped witness run over it (fresh records
// still serve whole-tree, only stale subjects inside the scope
// execute). An empty scope means no bound witness can move the
// caller's judgment, so no witness evidence is taken and no policy is
// needed; a nil scope is the caller's records-only judgment — unlike
// the whole-tree pass above, where a nil scope handed to the run means
// the whole policy. The returned run is nil whenever nothing ran, the
// one spelling of "unwitnessed" a coverage caller reads.
func Scoped(ctx context.Context, d verbcore.Deps, prepared *check.Prepared, scope map[gofresh.Subject]bool, why string) (*verify.Report, *verify.TestRun, error) {
	rep := progress.FromContext(ctx)
	spec, store := prepared.Spec, prepared.Store
	capture := len(scope) > 0
	var pc *golang.Capture
	var err error
	if capture {
		if pc, err = d.Capture(ctx); err != nil {
			return nil, nil, err
		}
	}
	symbols, err := golang.OperationSymbols(ctx, store, pc)
	if err != nil {
		return nil, nil, err
	}
	backends, err := d.Backends(ctx, symbols)
	if err != nil {
		return nil, nil, err
	}
	defer verify.CloseBackends(backends)
	var tr *verify.TestRun
	if capture {
		rep.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
		if tr, err = d.RunTests(ctx, pc, verify.SeedingOf(backends), scope, why); err != nil {
			return nil, nil, err
		}
	}
	rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	return verify.Run(spec, store, backends, tr), tr, nil
}

// Gaps is the gap list's pass (REQ-gap-list): the prepared corpus and,
// when it holds gap records, the report and the coverage evaluated over
// witness evidence taken exactly as resolved-record pruning takes it —
// the gap-relevant scope, no witness evidence when no bound witness can
// move a gap-relevant bucket. A store without gap records skips the
// witness evidence, never the compilation: the coverage is nil.
// Verification problems ride the report as the caller's caveat, never
// a refusal.
func Gaps(ctx context.Context, d verbcore.Deps) (*check.Prepared, *verify.Report, *coverage.Report, error) {
	rep := progress.FromContext(ctx)
	rep.Phase(stipulatorv1.Phase_PHASE_COMPILE)
	prepared, err := d.Prepare()
	if err != nil {
		return nil, nil, nil, err
	}
	spec, store, pol := prepared.Spec, prepared.Store, prepared.Coverage
	if len(store.Gaps) == 0 {
		return prepared, nil, nil, nil
	}
	scope, gapIds, err := check.GapScope(spec, store)
	if err != nil {
		return nil, nil, nil, err
	}
	why := fmt.Sprintf("scoped to %d gapped requirements", len(gapIds))
	report, tr, err := Scoped(ctx, d, prepared, scope, why)
	if err != nil {
		return nil, nil, nil, err
	}
	rep.Phase(stipulatorv1.Phase_PHASE_COVERAGE)
	return prepared, report, coverage.Evaluate(spec, report, store, tr != nil, pol), nil
}
