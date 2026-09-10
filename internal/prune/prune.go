// Package prune is the one core of the prune verb: the three modes —
// the witness store's garbage collection, the dangling-gap repair, and
// the resolved-gap evaluation — computed once for both faces, which
// supply what differs between them (the record store, the preparation,
// the backends, the witness run, the progress channel) and render the
// results their own way. The core computes and never writes: each face
// applies the computed updates through its own write seam.
package prune

import (
	"context"
	"errors"
	"fmt"

	"github.com/greatliontech/gofresh"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// Deps is what a face supplies to the core.
type Deps struct {
	// Root is the tree root: the witness store's and the resolution
	// records' coordinate.
	Root string
	// Prepare compiles the corpus and loads the records (the face's
	// preparation, with its own diagnostics rendering).
	Prepare func() (*check.Prepared, error)
	// Compile compiles the corpus alone — the dangling repair judges
	// against the compiled corpus and the records, nothing else, so it
	// never meets the preparation's other refusals.
	Compile func() (*stipulatorv1.Spec, error)
	// Load reads the records alone — the store mode judges from records
	// before any compilation, so a broken spec never blocks cost cleanup.
	Load func() (*records.Store, error)
	// Capture loads the accepted policy for a witnessed evaluation.
	Capture func(context.Context) (*golang.Capture, error)
	// Backends builds the verification backends over the operation's
	// symbols.
	Backends func(context.Context, []string) (map[string]verify.Backend, error)
	// RunTests executes the scoped witness run; why names the scope for
	// a face that announces it.
	RunTests func(ctx context.Context, pc *golang.Capture, seeding verify.WitnessSeeding, scope map[gofresh.Subject]bool, why string) (*verify.TestRun, error)
	// Phase reports a progress phase; nil on a face without a progress
	// channel.
	Phase func(stipulatorv1.Phase)
}

func (d Deps) phase(p stipulatorv1.Phase) {
	if d.Phase != nil {
		d.Phase(p)
	}
}

// Mode is the verb's mode selection. Store composes with nothing else.
type Mode struct {
	Check, NoTest, Dangling, Store bool
}

// Validate refuses a composition the verb has no meaning for.
func (m Mode) Validate() error {
	if m.Store && (m.Check || m.Dangling || m.NoTest) {
		return errors.New("prune: store composes with no other prune mode or flag")
	}
	return nil
}

// ProblemsError is the refusal a verification problem raises: a
// resolved gap is derived from coverage, which is only sound when
// verification is clean, so the core refuses rather than deletes on a
// shaky reading. Each face renders the problems its own way.
type ProblemsError struct {
	Problems []verify.Problem
}

func (e *ProblemsError) Error() string {
	return fmt.Sprintf("verification problems (%d); fix them first", len(e.Problems))
}

// StoreResult is the store mode's outcome: the witness-store counts and,
// under a captured policy, the resolution records' counts.
type StoreResult struct {
	Removed, Kept int
	// Resolutions is nil when no policy could be captured: the
	// resolution records are judged only under one, since the witness
	// subjects come from it.
	Resolutions *ResolutionCounts
}

// ResolutionCounts are the resolution records removed and kept.
type ResolutionCounts struct {
	Removed, Kept int
}

// StoreGC garbage-collects this corpus's witness store: the current
// bound tests-role symbols ARE the obligation universe, matched by exact
// record-key equality — no symbol parsing — and it runs only as this
// explicit mode, never opportunistically: an identity absent from THIS
// tree state may be live on another branch, and silent eviction would
// undo the variant store's branch-alternation serving
// (REQ-evidence-store-gc). It judges from records alone — before
// compilation, so a broken spec never blocks cost cleanup.
func StoreGC(ctx context.Context, d Deps) (StoreResult, error) {
	store, err := d.Load()
	if err != nil {
		return StoreResult{}, err
	}
	live := map[string]bool{}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if b.GetRole() == stipulatorv1.BindingRole_BINDING_ROLE_TESTS {
				live[b.GetSymbol()] = true
			}
		}
	}
	var liveGroup func(string) bool
	// A policy the operation cannot capture keeps every coordinate:
	// cost cleanup never guesses.
	pc, cerr := d.Capture(ctx)
	if cerr == nil && pc != nil {
		digests := golang.LiveGroupDigests(pc)
		liveGroup = func(group string) bool { return digests[group] }
	}
	removed, kept, err := witnesscache.GC(d.Root, func(pkg, test string) bool {
		return live[pkg+"."+test]
	}, liveGroup)
	if err != nil {
		return StoreResult{}, err
	}
	out := StoreResult{Removed: removed, Kept: kept}
	// The resolution records beside them: a symbol no binding names
	// and no witness subject carries serves no operation — judged only
	// under a captured policy, since the witness subjects come from it.
	if cerr == nil && pc != nil {
		resolutionsRemoved, resolutionsKept, err := golang.GCResolutions(ctx, d.Root, store, pc)
		if err != nil {
			return StoreResult{}, err
		}
		out.Resolutions = &ResolutionCounts{Removed: resolutionsRemoved, Kept: resolutionsKept}
	}
	return out, nil
}

// Dangling computes the dangling-gap deletions: danglingness is a
// corpus-and-records fact — no witnesses, no symbol resolution, and no
// verification gate, since a dangling gap IS a verification problem and
// gating its repair on clean verification would deadlock the repair
// (REQ-gap-prune-dangling).
func Dangling(d Deps) ([]author.Update, error) {
	spec, err := d.Compile()
	if err != nil {
		return nil, err
	}
	store, err := d.Load()
	if err != nil {
		return nil, err
	}
	return author.PruneDanglingGaps(store, records.HashesOf(spec)), nil
}

// Resolved is the resolved-gap evaluation's outcome.
type Resolved struct {
	// Gaps is the gap-record count the evaluation read.
	Gaps int
	// Evaluated is false on the deletion-only fast path: no gap records
	// means nothing can resolve, so no witness evidence is gathered at
	// all (REQ-gap-resolved-pruned).
	Evaluated bool
	// Served and Executed are the witness run's counts; zero without a
	// run.
	Served, Executed int
	// Prunes are the resolved gap records' deletions.
	Prunes []author.Update
}

// Line names the evaluation performed — the gap-record count and the
// served and executed witness counts (REQ-gap-resolved-pruned) — one
// spelling for both faces.
func (r Resolved) Line() string {
	return fmt.Sprintf("evaluated %d gap records: %d witnesses served, %d executed", r.Gaps, r.Served, r.Executed)
}

// Evaluate computes the resolved-gap deletions. The evaluation takes its
// witness evidence from the serving class, narrowed to the subjects the
// gapped requirements bind (REQ-gap-resolved-pruned); noTest is the
// caller's records-only judgment, no witness run at all.
func Evaluate(ctx context.Context, d Deps, noTest bool) (Resolved, error) {
	d.phase(stipulatorv1.Phase_PHASE_COMPILE)
	prepared, err := d.Prepare()
	if err != nil {
		return Resolved{}, err
	}
	spec, store, pol := prepared.Spec, prepared.Store, prepared.Coverage
	if len(store.Gaps) == 0 {
		return Resolved{}, nil
	}
	// A resolved gap is derived from coverage, which is only sound when
	// verification is clean: the record-only half refuses before any
	// child process (REQ-check-preparation).
	if len(prepared.Hygiene) > 0 {
		return Resolved{}, &ProblemsError{Problems: prepared.Hygiene}
	}
	var pc *golang.Capture
	var scope map[gofresh.Subject]bool
	var gapIds []string
	if !noTest {
		// Resolution reads the gapped requirements' coverage — and, for
		// a gap with a covered(<id>) landing condition, the condition
		// target's coverage — so the stale-remainder execution narrows
		// to those requirements' bound subjects. A gap id outside the
		// corpus is dangling — never resolvable, owned by the explicit
		// dangling mode — filtered rather than refused; the dangling
		// record still surfaces as a verification problem below.
		if scope, gapIds, err = check.GapScope(spec, store); err != nil {
			return Resolved{}, err
		}
		if pc, err = d.Capture(ctx); err != nil {
			return Resolved{}, err
		}
	}
	symbols, err := golang.OperationSymbols(ctx, store, pc)
	if err != nil {
		return Resolved{}, err
	}
	backends, err := d.Backends(ctx, symbols)
	if err != nil {
		return Resolved{}, err
	}
	defer verify.CloseBackends(backends)
	var tr *verify.TestRun
	if !noTest {
		d.phase(stipulatorv1.Phase_PHASE_EXECUTION)
		why := fmt.Sprintf("scoped to %d gapped requirements", len(gapIds))
		if tr, err = d.RunTests(ctx, pc, verify.SeedingOf(backends), scope, why); err != nil {
			return Resolved{}, err
		}
		// The resolved-record evaluation is pinned to the serving class
		// (REQ-gap-resolved-pruned); the producer's mark makes a wrong
		// witness source a loud refusal.
		if err := verify.ServingClassRequired(tr); err != nil {
			return Resolved{}, err
		}
	}
	d.phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	rep := verify.Run(spec, store, backends, tr)
	if len(rep.Problems) > 0 {
		return Resolved{}, &ProblemsError{Problems: rep.Problems}
	}
	d.phase(stipulatorv1.Phase_PHASE_COVERAGE)
	cov := coverage.Evaluate(spec, rep, store, !noTest, pol)
	resolved := map[string]bool{}
	for _, g := range cov.Gaps {
		if g.State == coverage.Resolved {
			resolved[g.RequirementId] = true
		}
	}
	out := Resolved{Gaps: len(store.Gaps), Evaluated: true, Prunes: author.PruneResolvedGaps(store, resolved)}
	if tr != nil {
		out.Served, out.Executed = tr.Fresh, tr.Ran
	}
	return out, nil
}
