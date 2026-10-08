package golang

import (
	"context"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// packageLeg is one package's publication state within a witness group,
// on either form. The package is the unit of persistence
// (REQ-policy-cancellation), so it is the unit of validation too: its
// serving checks, captures, proof leg, publish and revalidation run on
// its own sibling of the group's one analysis view — the expensive
// observation, paid once per group — so under the deferred-close engine
// its verdicts are provisional until ITS validation, and that
// validation re-observes the package's subjects alone, never the
// group's.
type packageLeg struct {
	// view is the package's sibling of the group's view; nil once the
	// leg has released.
	view *gofresh.View
	// candidates, observed and observedFPs carry the package's
	// observation-completeness proof leg: the subjects whose process is
	// predicted to run exactly one top-level runnable, their proofs
	// captured on a sibling of view before execution and attached and
	// validated when the package publishes.
	candidates  []gofresh.Subject
	observed    *gofresh.View
	observedFPs map[gofresh.Subject]gofresh.Fingerprint
	// published is the selective form's mark that the package's executed
	// records published — at its completion during execution, so the
	// verification pass publishes only the packages nothing executed.
	// The health-judged form publishes each package exactly once, at
	// its completion, and never reads it.
	published bool
}

// newPackageLeg derives one package's leg from the group's view: a
// sibling over the package's subjects, every fact the parent's.
func newPackageLeg(parent *gofresh.View, subjects []gofresh.Subject) (*packageLeg, error) {
	view, err := parent.Sibling(subjects)
	if err != nil {
		return nil, err
	}
	return &packageLeg{view: view}, nil
}

// prove captures the leg's observation-completeness proofs for the
// given candidates, on a sibling of the leg's own view; a failed
// derivation leaves the ordinary captures in force.
func (l *packageLeg) prove(ctx context.Context, candidates []gofresh.Subject) {
	l.candidates = candidates
	l.observed, l.observedFPs = observedView(ctx, l.view, candidates)
}

// release drops the leg's views once nothing more reads them; the
// published and revalidated marks stand.
func (l *packageLeg) release() {
	l.view, l.observed, l.observedFPs = nil, nil, nil
}

// legsReleased reports whether every leg has released — the moment the
// group's own view may go too.
func legsReleased(legs map[string]*packageLeg) bool {
	for _, l := range legs {
		if l.view != nil {
			return false
		}
	}
	return true
}

// installRecords offers the records to the store and returns the ones
// that landed; a record the store refused is not cached, and its
// subject's reason names the store's fault — a filesystem remedy,
// never the evidence's. One install path for both forms, so the
// account and the store never disagree.
func installRecords(dir string, records []witnesscache.Record, reasons map[gofresh.Subject]uncacheable) []witnesscache.Record {
	var installed []witnesscache.Record
	for _, rec := range records {
		if err := witnesscache.Install(dir, rec); err != nil {
			reasons[gofresh.Subject{Package: rec.Package, Symbol: rec.Test}] = reasonStoreRefused.with(err.Error())
			continue
		}
		installed = append(installed, rec)
	}
	return installed
}
