// Package verifyrun is the verification pass every verb that judges a
// tree runs first — verify, gate, context, partitions — computed once
// for both faces: the prepared corpus, the report, and the witness run
// the report was correlated with.
package verifyrun

import (
	"context"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
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
