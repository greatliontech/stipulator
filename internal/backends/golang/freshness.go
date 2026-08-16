package golang

import (
	"context"
	"maps"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// Freshness helpers shared by the witness surfaces: the selective
// witness runner (witnessrun.go) and the check path's witness recorder
// (derive.go) both check recorded fingerprints, select
// observation-completeness proofs, and validate completed observations
// through these seams, so the two paths cannot drift in what proves
// equivalence (REQ-evidence-witness-freshness).

// checkFingerprints checks a recording set with one shared drift bracket pair,
// runtime window, and precise analysis per policy class: observed recordings
// batch through the observed policy, the rest through the ordinary hierarchical
// policy. Per-record checking multiplied full workspace observations by the
// record count.
// checkFingerprints is a variable so the post-run check's FAULT arm -
// an infrastructure failure no fixture can produce - is testable: the
// run-degradation surface it feeds is contract
// (REQ-evidence-freshness-degrade).
var checkFingerprints = func(ctx context.Context, view *gofresh.View, recorded map[gofresh.Subject]gofresh.Fingerprint) (map[gofresh.Subject]gofresh.Verdict, error) {
	observed := make(map[gofresh.Subject]gofresh.Fingerprint, len(recorded))
	plain := make(map[gofresh.Subject]gofresh.Fingerprint, len(recorded))
	for subject, fingerprint := range recorded {
		if fingerprint.ObservationAssertion != "" || fingerprint.ObservationProof != (gofresh.ObservationProof{}) {
			observed[subject] = fingerprint
		} else {
			plain[subject] = fingerprint
		}
	}
	verdicts := make(map[gofresh.Subject]gofresh.Verdict, len(recorded))
	if len(observed) != 0 {
		batch, err := view.CheckObservedBatch(ctx, observed)
		if err != nil {
			return nil, err
		}
		maps.Copy(verdicts, batch)
	}
	if len(plain) != 0 {
		batch, err := view.CheckBatch(ctx, plain)
		if err != nil {
			return nil, err
		}
		maps.Copy(verdicts, batch)
	}
	return verdicts, nil
}

func validatedObservation(fingerprint gofresh.Fingerprint, state runtimeinput.State) bool {
	return fingerprint.ObservationProof.Observable && !state.Unverifiable
}

// observedView selects observation-completeness proof for every unasserted
// subject in one batch, on a sibling of the group's own analysis view:
// the derivation itself observes nothing — the sibling serves the
// parent's one observation, so the proof leg's evidence and the group's
// share one tree generation by construction — while the proof capture
// still runs its own bracketed pass to make the new proofs record-grade,
// and the sibling owns its attach/validate transaction. Failure leaves
// the ordinary maximal captures in force.
func observedView(ctx context.Context, parent *gofresh.View, subjects []gofresh.Subject) (*gofresh.View, map[gofresh.Subject]gofresh.Fingerprint) {
	if len(subjects) == 0 {
		return nil, nil
	}
	view, err := parent.Sibling(subjects)
	if err != nil {
		return nil, nil
	}
	captured, err := view.CaptureObservedBatch(ctx)
	if err != nil {
		return nil, nil
	}
	return view, captured
}

// topLevel is the top-level test name of a possibly-subtest path.
func topLevel(test string) string {
	if i := strings.Index(test, "/"); i >= 0 {
		return test[:i]
	}
	return test
}

// outcomeFromString maps a cached outcome back to the verify enum; an
// unknown word reads as not-run, which the correlator treats as
// unwitnessed — the conservative direction.
func outcomeFromString(s string) verify.TestOutcome {
	switch s {
	case "passed":
		return verify.TestPassed
	case "failed":
		return verify.TestFailed
	case "skipped":
		return verify.TestSkipped
	}
	return verify.TestNotRun
}

// assembleWitnessRecord builds one publishable witness record: the
// compartment ledger reads from the same view snapshot the fingerprint's
// compartment hash pinned (the inert-growth carve-out's diff base,
// REQ-evidence-witness-freshness), and the registrations sort canonically.
// A subject the view cannot ledger returns false — it stays unpublishable.
func assembleWitnessRecord(view *gofresh.View, s gofresh.Subject, fp gofresh.Fingerprint, outcomes map[string]string, regs []verify.Registration, exclusions []string) (witnesscache.Record, bool) {
	ledger, err := view.TestVariantLedger(s)
	if err != nil {
		return witnesscache.Record{}, false
	}
	sorted := append([]verify.Registration(nil), regs...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Test != b.Test {
			return a.Test < b.Test
		}
		return a.Requirement < b.Requirement
	})
	return witnesscache.Record{
		Package:               s.Package,
		Test:                  s.Symbol,
		Fingerprint:           witnesscache.FromGofresh(fp),
		CompartmentLedger:     witnesscache.LedgerFromGofresh(ledger),
		Outcomes:              outcomes,
		Regs:                  compactRegs(sorted),
		ObservationExclusions: exclusions,
	}, true
}
