package golang

import (
	"context"
	"maps"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// pubSubject is one eligible subject's cache material on the shared
// publication ladder: the granting process's owned observation, the
// subject's outcomes and registrations from that process alone, and
// whether the subject ran alone in its process (the proof leg's
// precondition). Both eligibility judges - the full-execution path's
// per-package facts and the selective path's per-process grantingRun -
// produce this one shape.
type pubSubject struct {
	obs      *ProcessObservation
	outcomes map[string]string
	regs     []verify.Registration
	solo     bool
}

// publishEligible is the one publication ladder for every path that
// produces witness records - full execution, selective serving, and the
// drift retry: the proof leg (attach + observed-view close), final
// fingerprint assembly, the post-run producer check, the group's ONE
// closing validation (gating served outcomes and publication alike),
// and record assembly. Eligibility stays with the caller - it is the
// only stage whose unit of judgment differs per path
// (REQ-evidence-witness-freshness names one publication concept and one
// post-run revalidation; the ladder is that concept's one mechanism).
//
// On a post-run check fault or a closing refusal the ladder fills
// per-subject reasons (the spec's diagnosable set) and additionally
// returns the fault as checkFault/closeFault so a caller with a
// run-level degradation surface can name it there instead. discarded
// reports that the close refused with served outcomes at stake: the
// caller must treat every serve as discarded.
func publishEligible(
	ctx context.Context,
	group string,
	view, observed *gofresh.View,
	observedFPs map[gofresh.Subject]gofresh.Fingerprint,
	candidates []gofresh.Subject,
	order []gofresh.Subject,
	eligible map[gofresh.Subject]*pubSubject,
	fps map[gofresh.Subject]gofresh.Fingerprint,
	excludedPaths []string,
	served []gofresh.Subject,
	executedWhy map[gofresh.Subject]string,
	reasons map[gofresh.Subject]string,
) (records []witnesscache.Record, discarded bool, checkFault, closeFault, fatal error) {
	// Observation-completeness proofs attach only when every candidate of
	// the group can attach: the observed view revalidates as one unit, so
	// a single candidate whose process left no completed observation (or
	// did not run its subject alone) drops the proof leg whole and every
	// candidate falls back to the plain per-process manifest.
	attached := map[gofresh.Subject]gofresh.Fingerprint{}
	attachedValid := map[gofresh.Subject]bool{}
	if observed != nil && len(observedFPs) == len(candidates) {
		complete := true
		for _, s := range candidates {
			ps, ok := eligible[s]
			if !ok || !ps.solo {
				complete = false
				break
			}
			fp, err := observed.AttachObservation(s, observedFPs[s], ps.obs.Runtime)
			if err != nil {
				complete = false
				break
			}
			state, err := runtimeinput.CompletedState(ps.obs.Runtime)
			if err != nil {
				complete = false
				break
			}
			attached[s] = fp
			attachedValid[s] = validatedObservation(fp, state)
			if !attachedValid[s] {
				// The sealed state names the concrete input; the proof
				// refusal names an analyzer class. Prefer the input.
				switch {
				case state.Unverifiable:
					reasons[s] = "observation sealed: " + state.Reason
				case !fp.ObservationProof.Observable:
					reasons[s] = "observation proof refused: " + fp.ObservationProof.Reason
				}
			}
		}
		if complete && len(candidates) > 0 {
			if err := observed.Validate(ctx); err != nil {
				if ctx.Err() != nil {
					return nil, false, nil, nil, ctx.Err()
				}
				complete = false
			}
		}
		if !complete {
			attached = map[gofresh.Subject]gofresh.Fingerprint{}
			attachedValid = map[gofresh.Subject]bool{}
		}
	}

	// Finalize fingerprints: the proof-attached form where it validated,
	// otherwise the plain form carrying the producing process's own
	// runtime-input manifest.
	final := map[gofresh.Subject]gofresh.Fingerprint{}
	for _, s := range order {
		ps := eligible[s]
		if ps == nil {
			continue
		}
		if fp, ok := attached[s]; ok {
			final[s] = fp
			continue
		}
		fp := fps[s]
		if fp.ObservationAssertion == "" {
			state, err := runtimeinput.CompletedState(ps.obs.Runtime)
			if err != nil {
				reasons[s] = "observation state unavailable: " + err.Error()
				continue
			}
			fp.RuntimeInputs, fp.RuntimeDigest = state.Manifest, state.Digest
		}
		final[s] = fp
	}

	// Runtime producer validation: each record publishes only when its
	// post-run check returns valid against the current tree. A stale
	// verdict is a mid-run drift of the record's source or runtime
	// inputs - the executed outcome stands, the record is dropped so the
	// next run re-derives it; an unverifiable verdict can never check
	// valid and is dropped the same way. Both are visible as the
	// uncacheable count, never silence.
	verdicts := map[gofresh.Subject]gofresh.Verdict{}
	unvalidated := map[gofresh.Subject]gofresh.Fingerprint{}
	for s, fp := range final {
		if attachedValid[s] {
			verdicts[s] = gofresh.Verdict{Status: gofresh.Valid}
		} else {
			unvalidated[s] = fp
		}
	}
	checked, err := checkFingerprints(ctx, view, unvalidated)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, nil, nil, ctx.Err()
		}
		// A faulting post-run check publishes nothing for the group; the
		// executed evidence stands and every subject counts uncacheable.
		// Served outcomes still need their seal, so the close below runs
		// when they are at stake.
		for _, s := range order {
			if _, ok := reasons[s]; !ok {
				reasons[s] = "post-run producer validation faulted: " + err.Error()
			}
		}
		d, cErr, fErr := closeGroup(ctx, view, len(served) != 0, order, served, executedWhy, reasons)
		return nil, d, err, cErr, fErr
	}
	maps.Copy(verdicts, checked)
	for s, v := range checked {
		if v.Status != gofresh.Valid {
			if _, ok := reasons[s]; !ok {
				// The verdict's own reason carries gofresh's attribution -
				// moved inputs named per identity.
				reasons[s] = "post-run validation: " + v.Reason
			}
		}
	}
	// The one deferred close per group: the view's validation is the
	// closing observation for the served re-check AND the post-run
	// publish checks alike - no served outcome stands and no record
	// publishes unless the tree still agrees, and a refusal discards
	// every provisional verdict exactly as gofresh's deferred-close
	// contract requires. The executed evidence itself stands untouched.
	// With nothing served, nothing checked, and nothing publishable
	// there is no window to close: the fully-warm no-serve pass pays no
	// observation here.
	d, cErr, fErr := closeGroup(ctx, view, len(final) != 0 || len(served) != 0, order, served, executedWhy, reasons)
	if fErr != nil {
		return nil, false, nil, nil, fErr
	}
	if d || cErr != nil {
		return nil, d, nil, cErr, nil
	}

	for _, s := range order {
		fp, ok := final[s]
		if !ok || verdicts[s].Status != gofresh.Valid {
			continue
		}
		ps := eligible[s]
		rec, ok := assembleWitnessRecord(group, view, s, fp, ps.outcomes, ps.regs, excludedPaths)
		if !ok {
			continue
		}
		records = append(records, rec)
		delete(reasons, s)
	}
	for _, s := range order {
		if _, published := final[s]; published && verdicts[s].Status == gofresh.Valid {
			continue
		}
		if _, ok := reasons[s]; !ok {
			reasons[s] = "record not published"
		}
	}
	return records, false, nil, nil, nil
}

// closeGroup runs the group's one closing validation when anything is at
// stake, filling refusal reasons on every unsatisfied stale subject and
// discarding served outcomes with the named cause. It reports the
// served discard and the close error separately so run-degradation
// surfaces can name the cause.
func closeGroup(ctx context.Context, view *gofresh.View, stake bool, order, served []gofresh.Subject, executedWhy map[gofresh.Subject]string, reasons map[gofresh.Subject]string) (discarded bool, closeErr, fatal error) {
	if !stake {
		return false, nil, nil
	}
	if err := view.Validate(ctx); err != nil {
		if ctx.Err() != nil {
			return false, nil, ctx.Err()
		}
		for _, s := range order {
			if _, ok := reasons[s]; !ok {
				reasons[s] = "source producer validation failed: " + err.Error()
			}
		}
		// Every discarded serve re-executes holding prior evidence, so
		// each names why serving refused it (the spec's attribution for
		// re-executed record holders).
		for _, s := range served {
			executedWhy[s] = "source producer validation failed: " + err.Error()
		}
		return len(served) != 0, err, nil
	}
	return false, nil, nil
}
