package golang

import (
	"context"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// pubSubject contains only material from one independently eligible execution.
// Gofresh owns evidence selection and finalization; this layer owns the outcome
// gate and durable record, never a second native evidence ladder.
type pubSubject struct {
	obs         *ProcessObservation
	fingerprint gofresh.Fingerprint
	outcomes    map[string]string
	regs        []verify.Registration
}

func publishEligible(ctx context.Context, group string, leg *packageLeg, order []gofresh.Subject, eligible map[gofresh.Subject]*pubSubject, declarations recordedDeclarations, reasons map[gofresh.Subject]uncacheable) ([]witnesscache.Record, error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := leg.validateInputs(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		for _, s := range order {
			reasons[s] = reasonSourceFailed.with(err.Error())
		}
		return nil, err, nil
	}
	var records []witnesscache.Record
	for _, s := range order {
		ps := eligible[s]
		if ps == nil {
			continue
		}
		rec, ok := assembleWitnessRecord(group, leg.view, s, ps.fingerprint, ps.outcomes, ps.regs, declarations)
		if !ok {
			reasons[s] = reasonNotPublished.with("native compartment ledger unavailable")
			continue
		}
		records = append(records, rec)
		delete(reasons, s)
	}
	return records, nil, nil
}
