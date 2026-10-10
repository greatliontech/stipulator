package golang

import (
	"context"
	"sync"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// packageLeg is one package's publication state within a witness group,
// on either form. The package is the unit of persistence
// (REQ-policy-cancellation-unit), so each closing validation re-observes
// the package's subjects alone. Its view is a sibling of the group's one
// analysis view, sharing the expensive observation. Serving obligations
// remain on it until all executions finish. Publication of an executing
// sibling uses a separate sibling over those same facts, so its earlier
// close cannot seal the serving transaction. The proof-attachment leg
// likewise has its own view and closing validation.
type packageLeg struct {
	// preparation serializes spawn-side proof use with a run-wide degradation
	// releasing legs while sibling packages are still preparing their process.
	preparation sync.Mutex
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

// processProofs routes preparation to the same invocation-owned package leg
// that will attach and validate the resulting observation. The map is assembled
// before execution; each package exclusively owns its leg until publication.
type processProofs map[string]map[string]*packageLeg

func (p processProofs) add(g *captureGroup, legs map[string]*packageLeg) {
	for pkg, leg := range legs {
		inv := g.selectingInvocation(pkg)
		if inv == "" || g.packages[pkg].ambiguous {
			continue
		}
		if p[inv] == nil {
			p[inv] = map[string]*packageLeg{}
		}
		p[inv][pkg] = leg
	}
}

// prepareOutcome binds the already selected proof to this actual process span.
// Whole-package execution uses the discovery-time solo prediction; an explicit
// selection must name exactly that runnable. Isolation never borrows another
// runnable's support, and preparation failure leaves identity-only guards.
func (l *packageLeg) prepareOutcome(ctx context.Context, selection []string, frame runtimeinput.ProducerFrame, process string) runtimeinput.OutcomeSupport {
	if l == nil {
		return runtimeinput.OutcomeSupport{}
	}
	l.preparation.Lock()
	defer l.preparation.Unlock()
	if l.observed == nil || len(l.candidates) != 1 {
		return runtimeinput.OutcomeSupport{}
	}
	if selection != nil && (len(selection) != 1 || selection[0] != l.candidates[0].Symbol) {
		return runtimeinput.OutcomeSupport{}
	}
	support, err := l.observed.PrepareOutcomeSupport(ctx, frame, process)
	if err != nil {
		return runtimeinput.OutcomeSupport{}
	}
	return support
}

// release drops the leg's views once nothing more reads them; the
// published and revalidated marks stand.
func (l *packageLeg) release() {
	l.preparation.Lock()
	defer l.preparation.Unlock()
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
// account and the store never disagree. Cancellation returns the landed prefix
// and the context error, so the caller accounts for that prefix before ending.
func installRecords(ctx context.Context, dir string, records []witnesscache.Record, reasons map[gofresh.Subject]uncacheable) ([]witnesscache.Record, error) {
	var installed []witnesscache.Record
	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			return installed, err
		}
		if err := witnesscache.Install(ctx, dir, rec); err != nil {
			if ctx.Err() != nil {
				return installed, ctx.Err()
			}
			reasons[gofresh.Subject{Package: rec.Package, Symbol: rec.Test}] = reasonStoreRefused.with(err.Error())
			continue
		}
		installed = append(installed, rec)
	}
	return installed, ctx.Err()
}
