package golang

import (
	"context"
	"sync"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// packageLeg holds immutable preparation and an independently closing historical
// check. Each execution creates its own PackageUnit; publication cannot close the
// historical check, and isolation never borrows an earlier process's support.
type packageLeg struct {
	preparation sync.Mutex
	view        *witnessSnapshot
	binding     *gofresh.RuntimeBinding
	soloBinding *gofresh.RuntimeBinding
	// environment belongs to the operation, not source-memory release. A
	// sibling's publication fault must not remove an active process's files.
	environment  *retainedEnvironment
	checking     *gofresh.CheckingUnit
	soloChecking *gofresh.CheckingUnit
	chosen       map[gofresh.Subject]chosenWitness
	inputs       *pgoObservation
	inputErr     error
	published    bool
}

// chosenWitness keeps the consumer outcome record with the native operation
// slot that checked exactly that variant. Extensions do not replace this record
// until the checking unit returns its final endpoint and ledger.
type chosenWitness struct {
	index  int
	solo   bool
	record witnesscache.Record
}

// prepareBuild remembers a precompile refusal even after an input is restored.
func (l *packageLeg) prepareBuild(ctx context.Context) {
	if l == nil {
		return
	}
	l.preparation.Lock()
	defer l.preparation.Unlock()
	if l.inputErr == nil {
		l.inputErr = l.inputs.validate(ctx)
	}
}

func (l *packageLeg) validateInputs(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.preparation.Lock()
	defer l.preparation.Unlock()
	if l.inputErr == nil {
		l.inputErr = l.inputs.validate(ctx)
	}
	return l.inputErr
}

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

func (l *packageLeg) release() {
	l.preparation.Lock()
	defer l.preparation.Unlock()
	if l.checking != nil {
		l.checking.Abort()
	}
	if l.soloChecking != nil {
		l.soloChecking.Abort()
	}
	l.view, l.binding, l.soloBinding, l.checking, l.soloChecking = nil, nil, nil, nil, nil
}

func (l *packageLeg) dispose() {
	l.release()
	if l.environment != nil {
		_ = l.environment.Close()
		l.environment = nil
	}
}

func legsReleased(legs map[string]*packageLeg) bool {
	for _, l := range legs {
		if l.view != nil {
			return false
		}
	}
	return true
}

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
