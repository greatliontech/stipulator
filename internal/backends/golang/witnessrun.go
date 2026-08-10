package golang

import (
	"context"
	"maps"
	"runtime/debug"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// The selective witness runner is the freshness-aware witness surface of
// the accepted test policy: one run that serves every expected witness
// whose cached fingerprint proves equivalence and selectively executes
// the rest under their covering invocations (REQ-evidence-witness-freshness,
// REQ-core-one-execution's witness-only selective execution). It judges
// no health — per-process dispositions gate evidence and publication
// exactly as SelectionResult carries them, and no package or invocation
// health exists on this path (REQ-evidence-freshness-no-health). The
// expected witness set derives from policy discovery against the tree's
// obligation universe; a subject whose package exactly one invocation
// covers and that invocation is witness-ineligible is outside the policy — it
// neither serves nor executes, and the count of such subjects rides the
// result so the gap is a visible number, never silence
// (REQ-policy-conservation's visibility principle). A multiply-selected
// package's subjects execute every run under each covering invocation —
// eligible legs granting outcomes, ineligible legs contributing failures and
// registrations only: they cannot serve — a record has no per-invocation
// identity — but their witness evidence stays aligned with the
// health-judged form's. Visibility
// has two homes on the result: the counts (outside-policy, uncacheable)
// and the package-level failure-diagnostic rows — an envelope cutoff, a
// package abort, a build failure — so an expected subject denied an
// outcome is traceable to the process event that denied it. Any fault on
// the freshness path serves nothing and executes every covered subject:
// the full witnessing run is this runner with an empty served set
// (REQ-evidence-freshness-degrade).

// RunWitnesses performs one selective witness run of the tree at dir
// under its committed test policy. The policy loads through the one
// shared loading seam and any load failure — a record problem
// (policy.ErrRecord) and an operational fault alike — surfaces to the
// caller: witness execution consumes the accepted policy, never a
// fallback suite (REQ-policy-explicit). The result carries witness
// outcomes and registrations (served plus executed), the served and
// executed counts, the uncacheable count, the outside-policy count, and
// the degraded reason when the freshness path faulted.
func RunWitnesses(ctx context.Context, dir string) (*verify.TestRun, error) {
	p, _, err := policy.Load(dir, map[string]policy.Backend{"go": Policy{}})
	if err != nil {
		return nil, err
	}
	return runWitnesses(ctx, dir, p, nil)
}

// RunWitnessesPolicy is RunWitnesses over an already-loaded accepted
// policy — the unified check loads the policy once for its own verdict
// short-circuits and hands it through.
func RunWitnessesPolicy(ctx context.Context, dir string, p *stipulatorv1.TestPolicy) (*verify.TestRun, error) {
	return runWitnesses(ctx, dir, p, nil)
}

// RunWitnessesScoped is the witness-only selective execution narrowed
// further to a caller-named subject scope: fresh records still serve
// for the whole tree, and only stale subjects inside the scope execute
// - the remainder is recorded scope-skipped, never broken. The degraded
// path expands to the scope's own full execution, never the tree's
// (REQ-check-verdict's scoped class).
func RunWitnessesScoped(ctx context.Context, dir string, p *stipulatorv1.TestPolicy, scope map[gofresh.Subject]bool) (*verify.TestRun, error) {
	return runWitnesses(ctx, dir, p, scope)
}

// witnessGroup is one capture group's serving state: the analysis views
// over its in-policy subjects, the cached records found for them, and the
// served/stale partition the pre-execution fingerprint check produced.
type witnessGroup struct {
	g      *captureGroup
	engine *gofresh.Engine
	view   *gofresh.View
	// recorded holds the loadable cache record per subject, when one exists.
	recorded map[gofresh.Subject]witnesscache.Record
	// executedWhy names, per stale subject that held prior evidence, why
	// its variants failed to serve - the last-checked variant's verdict
	// reason, movers named by gofresh's attribution.
	executedWhy map[gofresh.Subject]string
	// served are the subjects whose recorded fingerprint checked valid
	// before execution; stale maps each package to its executing top-level
	// names, sorted.
	served []gofresh.Subject
	stale  map[string][]string
	// refreshed marks served subjects that rode the inert-growth carve-out
	// (REQ-evidence-witness-freshness): their record in `recorded` carries
	// the fingerprint refreshed to the current compartment plus the current
	// ledger, and installs after post-run revalidation keeps them.
	refreshed map[gofresh.Subject]bool
	// fps are the pre-execution captures for stale subjects: fingerprints
	// must pin the tree that compiles the binaries, so capturing after
	// execution would let a mid-run edit publish pre-edit outcomes under a
	// post-edit hash. Captured before, the same interleaving reads stale:
	// the safe direction.
	fps map[gofresh.Subject]gofresh.Fingerprint
	// candidates, observed, observedFPs carry the observation-completeness
	// proof leg, computed after the stale set is known: a selective
	// process running exactly one top-level runnable is proof-eligible,
	// because no sibling runnable in the process can contribute unrecorded
	// process state to the subject's outcome.
	candidates  []gofresh.Subject
	observed    *gofresh.View
	observedFPs map[gofresh.Subject]gofresh.Fingerprint
}

func runWitnesses(ctx context.Context, dir string, p *stipulatorv1.TestPolicy, scope map[gofresh.Subject]bool) (*verify.TestRun, error) {
	rep := progress.FromContext(ctx)
	rep.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	pc, err := capturePolicy(ctx, dir, p)
	if err != nil {
		return nil, err
	}
	// A universe fault is a freshness-path fault, not a selection fault:
	// selection needs only the policy's own discovery, and the universe
	// feeds the outside-policy accounting the degraded reason then names.
	// It degrades exactly as an engine or view fault does
	// (REQ-evidence-freshness-degrade) — capturePolicy faults, by
	// contrast, error, because without them nothing can execute.
	universe, universeErr := discoverUniverse(ctx, dir)
	if universeErr != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// The expected witness set: every Test and Fuzz obligation of the
	// tree's obligation universe and of each invocation's own discovery
	// (an invocation's build selection can discover tests the default
	// selection does not).
	expected := map[gofresh.Subject]bool{}
	addExpected := func(obs []Obligation) {
		for _, o := range obs {
			if o.Kind == ObligationTest || o.Kind == ObligationFuzz {
				expected[gofresh.Subject{Package: o.Package, Symbol: o.Name}] = true
			}
		}
	}
	addExpected(universe)
	normalized := map[string]*NormalizedInvocation{}
	for _, ic := range pc.invocations {
		addExpected(ic.obligations)
		normalized[ic.n.Name] = ic.n
	}

	// The in-policy subjects and their covering invocations: exactly the
	// capture groups' subjects (one covering race invocation across the
	// whole policy). Everything else expected is outside the policy.
	inPolicy := map[gofresh.Subject]bool{}
	covering := map[string]*NormalizedInvocation{}
	for _, g := range pc.groups {
		for _, s := range groupSubjects(g, pc.globalCount) {
			inPolicy[s] = true
			covering[s.Package] = normalized[g.pkgInv[s.Package]]
		}
	}
	// A multiply-selected package's subjects cannot serve — a witness
	// record has no per-invocation identity — but they must not read
	// unwitnessed either: the health-judged form derives their evidence
	// from every covering invocation, so the witness-evidence form
	// executes them under each covering invocation from that invocation's
	// own discovery (same-group duplicates included) and the merge takes
	// the worst outcome. Witness-eligible legs (race, or an explicit
	// plain-witness admission) grant outcomes; ineligible legs
	// contribute failures only — an unadmitted non-race pass never
	// grants witness evidence — keeping the two forms' evidence aligned
	// (REQ-check-verdict).
	multiSel := map[string]TestSelection{}
	multiIneligible := map[string]TestSelection{}
	multiExecuted := map[gofresh.Subject]bool{}
	multiRaceCovered := map[gofresh.Subject]bool{}
	multiEligibleCovered := map[gofresh.Subject]bool{}
	for _, ic := range pc.invocations {
		dst := multiSel
		if !ic.n.WitnessEligible() {
			dst = multiIneligible
		}
		for _, o := range ic.obligations {
			if o.Kind != ObligationTest && o.Kind != ObligationFuzz {
				continue
			}
			if pc.globalCount[o.Package] <= 1 {
				continue
			}
			sel := dst[ic.n.Name]
			if sel == nil {
				sel = TestSelection{}
				dst[ic.n.Name] = sel
			}
			sel[o.Package] = append(sel[o.Package], o.Name)
			multiExecuted[gofresh.Subject{Package: o.Package, Symbol: o.Name}] = true
			if ic.n.Race {
				multiRaceCovered[gofresh.Subject{Package: o.Package, Symbol: o.Name}] = true
			}
			if ic.n.WitnessEligible() {
				multiEligibleCovered[gofresh.Subject{Package: o.Package, Symbol: o.Name}] = true
			}
		}
	}
	// Outside the witness-eligible selection means no eligible coverage
	// anywhere: a multiply-selected subject whose every covering
	// invocation is ineligible still executes (failures count), but its
	// legs can never grant a witness outcome, so it is outside exactly as
	// an unselected subject is (REQ-check-witness-selection).
	outside := 0
	outsideSubjects := map[string]bool{}
	for s := range expected {
		if !inPolicy[s] && !multiEligibleCovered[s] {
			outside++
			outsideSubjects[s.Package+"."+s.Symbol] = true
		}
	}

	cached := witnesscache.Load(dir)
	// One identity may hold several tree-state variants; at most one can
	// prove equivalent against the current tree, so serving tries each.
	cachedByKey := map[string][]witnesscache.Record{}
	for _, rec := range cached {
		cachedByKey[rec.Key()] = append(cachedByKey[rec.Key()], rec)
	}

	var groups []*witnessGroup
	degraded := ""
	if universeErr != nil {
		degraded = universeErr.Error()
	} else {
		groups, degraded = prepareWitnessGroups(ctx, dir, pc, cachedByKey)
		if degraded != "" && ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	// The executing selection per invocation: the stale subjects under
	// their covering invocations — or, degraded, every in-policy subject:
	// serving saves work, it never blocks or weakens witnessing.
	staleSel := map[string]TestSelection{}
	scopeSkipped := map[string]bool{}
	addStale := func(s gofresh.Subject) {
		if scope != nil && !scope[s] {
			// A stale subject outside the caller's scope is skipped,
			// recorded so its bindings read scope-skipped rather than
			// broken; on the degraded path this same gate makes the
			// fallback the scope's own full execution.
			scopeSkipped[s.Package+"."+s.Symbol] = true
			return
		}
		n := covering[s.Package]
		sel := staleSel[n.Name]
		if sel == nil {
			sel = TestSelection{}
			staleSel[n.Name] = sel
		}
		sel[s.Package] = append(sel[s.Package], s.Symbol)
	}
	if degraded != "" {
		for s := range inPolicy {
			addStale(s)
		}
	} else {
		for _, wg := range groups {
			for pkg, names := range wg.stale {
				for _, name := range names {
					addStale(gofresh.Subject{Package: pkg, Symbol: name})
				}
			}
		}
	}
	// Multiply-selected subjects execute every run, under each covering
	// eligible invocation, alongside that invocation's stale selection.
	for inv, sel := range multiSel {
		dst := staleSel[inv]
		if dst == nil {
			dst = TestSelection{}
			staleSel[inv] = dst
		}
		for pkg, names := range sel {
			for _, name := range names {
				if scope != nil && !scope[gofresh.Subject{Package: pkg, Symbol: name}] {
					scopeSkipped[pkg+"."+name] = true
					continue
				}
				dst[pkg] = append(dst[pkg], name)
			}
		}
		if len(dst) == 0 {
			delete(staleSel, inv)
		}
	}
	for _, sel := range staleSel {
		for pkg := range sel {
			sort.Strings(sel[pkg])
		}
	}

	// Release transient package-loading memory before spawning
	// race-instrumented builds; the views stay alive for post-execution
	// revalidation.
	debug.FreeOSMemory()
	rep.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	m := newExecMerge()
	// Completed-group durability: each witness group finishes and
	// installs the moment its last covering invocation completes, while
	// later invocations still execute - a run dying mid-execution keeps
	// every record already produced. Groups with nothing executing (all
	// served) finish in the verification pass; the degraded path
	// executes with no groups and publishes nothing.
	var published []witnesscache.Record
	uncacheableWhy := map[gofresh.Subject]string{}
	driftedByGroup := map[*witnessGroup][]gofresh.Subject{}
	finished := map[*witnessGroup]bool{}
	installNow := func(records []witnesscache.Record) {
		for _, rec := range records {
			_ = witnesscache.Install(dir, rec)
		}
	}
	pendingInvs := map[*witnessGroup]map[string]bool{}
	invGroups := map[string][]*witnessGroup{}
	for _, wg := range groups {
		for pkg := range wg.stale {
			n := covering[pkg]
			if n == nil {
				// Defensive only: every stale subject's covering
				// invocation was dereferenced when its selection was
				// built.
				continue
			}
			if pendingInvs[wg] == nil {
				pendingInvs[wg] = map[string]bool{}
			}
			if !pendingInvs[wg][n.Name] {
				pendingInvs[wg][n.Name] = true
				invGroups[n.Name] = append(invGroups[n.Name], wg)
			}
		}
	}
	onInvocationDone := func(name string) error {
		for _, wg := range invGroups[name] {
			delete(pendingInvs[wg], name)
			if len(pendingInvs[wg]) > 0 || finished[wg] {
				continue
			}
			groupDrifted, records, reasons, err := finishGroup(ctx, wg, m)
			if err != nil {
				return err
			}
			maps.Copy(uncacheableWhy, reasons)
			published = append(published, records...)
			driftedByGroup[wg] = groupDrifted
			finished[wg] = true
			installNow(records)
		}
		return nil
	}
	if err := executeSelections(ctx, p, normalized, staleSel, m, onInvocationDone); err != nil {
		return nil, err
	}
	var ineligibleMerge *execMerge
	if len(multiIneligible) > 0 {
		// Ineligible covering legs of multiply-selected packages: their
		// failures are evidence the health-judged form would surface, so
		// they execute here in the execution phase and fold after the
		// main merges — failures and registrations only, never a grant.
		ineligibleMerge = newExecMerge()
		if err := executeSelections(ctx, p, normalized, multiIneligible, ineligibleMerge, nil); err != nil {
			return nil, err
		}
	}

	rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	var servedRecords []witnesscache.Record
	plainServedKey := map[string]bool{}
	retryMerge := newExecMerge()
	if degraded == "" {
		var drifted []gofresh.Subject
		for _, wg := range groups {
			if !finished[wg] {
				// All-served groups finish here; executing groups
				// finished and installed at their last covering
				// invocation's completion.
				groupDrifted, records, reasons, err := finishGroup(ctx, wg, m)
				if err != nil {
					return nil, err
				}
				maps.Copy(uncacheableWhy, reasons)
				published = append(published, records...)
				driftedByGroup[wg] = groupDrifted
				installNow(records)
			}
			groupDrifted := driftedByGroup[wg]
			drifted = append(drifted, groupDrifted...)
			isDrifted := map[gofresh.Subject]bool{}
			for _, s := range groupDrifted {
				isDrifted[s] = true
			}
			for _, s := range wg.served {
				if !isDrifted[s] {
					servedRecords = append(servedRecords, wg.recorded[s])
					// The record fingerprints under its covering
					// invocation's build inputs, so the invocation's
					// tier is the served witness's tier.
					if n := covering[s.Package]; n != nil && n.PlainWitness {
						plainServedKey[s.Package+"."+s.Symbol] = true
					}
				}
			}
		}
		if len(drifted) > 0 {
			retryPublished, retryReasons, err := retryDrifted(ctx, dir, p, normalized, covering, driftedByGroup, retryMerge)
			if err != nil {
				return nil, err
			}
			maps.Copy(uncacheableWhy, retryReasons)
			published = append(published, retryPublished...)
			installNow(retryPublished)
		}
	}

	// Assemble the run's witness-evidence view: executed outcomes gated on
	// their producing process's disposition — a red process yields no
	// green evidence, and the isolation pass's solo processes carry their
	// own dispositions — then the surviving served records, whose keys are
	// disjoint from every executed subject's by construction.
	// The selective runner is the serving class by identity — the degraded
	// empty-served form included (REQ-gap-resolved-pruned's consumers
	// enforce the mark).
	tr := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}, RaceEnabled: true, PlainWitness: map[string]bool{}, OutsidePolicy: outside, OutsideSubjects: outsideSubjects, ScopeSkipped: scopeSkipped, SelectiveServing: true}
	// Tier attribution across every grant source: a key granted by any
	// race leg holds the race tier; a key granted only by plain-witness
	// admissions carries the recorded downgrade
	// (REQ-check-witness-selection).
	raceGranted := map[string]bool{}
	plainGranted := map[string]bool{}
	plainInv := map[string]bool{}
	for name, n := range normalized {
		plainInv[name] = n.PlainWitness
	}
	for _, wg := range groups {
		for s, why := range wg.executedWhy {
			if why == "" {
				why = "prior evidence stale"
			}
			if tr.ExecutedReasons == nil {
				tr.ExecutedReasons = map[string]string{}
			}
			tr.ExecutedReasons[s.Package+"."+s.Symbol] = why
		}
	}
	ranTop := map[string]bool{}
	consumeMerge(tr, m, ranTop, plainInv, raceGranted, plainGranted)
	consumeMerge(tr, retryMerge, ranTop, plainInv, raceGranted, plainGranted)
	if ineligibleMerge != nil {
		consumeMergeFailuresOnly(tr, ineligibleMerge, ranTop)
	}
	tr.Ran = len(ranTop)
	for _, rec := range servedRecords {
		plainRecord := plainServedKey[rec.Package+"."+rec.Test]
		for key, out := range rec.Outcomes {
			tr.Outcomes[key] = outcomeFromString(out)
			if plainRecord {
				plainGranted[key] = true
			} else {
				raceGranted[key] = true
			}
		}
		tr.Registrations = append(tr.Registrations, rec.Regs...)
		tr.Fresh++
	}
	for key, out := range tr.Outcomes {
		if out == verify.TestPassed && plainGranted[key] && !raceGranted[key] {
			tr.PlainWitness[key] = true
		}
	}
	sortRegs(tr)
	tr.Degraded = degraded
	// Uncached is structural — executed subjects minus records that
	// survived to publication — so every drop path counts: red or aborted
	// processes, missing observations, failed captures, drifted post-run
	// verdicts, and the degraded path (which publishes nothing) alike.
	if tr.Ran > len(published) {
		tr.Uncached = tr.Ran - len(published)
	}
	// Per-test attribution for the uncacheable set: the ladder's own
	// refusal reasons, the multiply-selected class, the degraded path,
	// and a structural fallback for anything a drop path missed
	// (REQ-evidence-witness-freshness's diagnosable-set requirement).
	if tr.Uncached > 0 || len(uncacheableWhy) > 0 {
		tr.UncacheableReasons = map[string]string{}
		for s, why := range uncacheableWhy {
			tr.UncacheableReasons[s.Package+"."+s.Symbol] = why
		}
		for s := range multiExecuted {
			if _, ok := tr.UncacheableReasons[s.Package+"."+s.Symbol]; !ok {
				tr.UncacheableReasons[s.Package+"."+s.Symbol] = "multiply-selected: a record has no per-invocation identity"
			}
		}
		publishedKey := map[string]bool{}
		for _, rec := range published {
			publishedKey[rec.Package+"."+rec.Test] = true
		}
		for key := range ranTop {
			if publishedKey[key] {
				delete(tr.UncacheableReasons, key)
				continue
			}
			if _, ok := tr.UncacheableReasons[key]; !ok {
				if degraded != "" {
					tr.UncacheableReasons[key] = "freshness path degraded: " + degraded
				} else {
					tr.UncacheableReasons[key] = "record not published"
				}
			}
		}
	}

	// Publication installed at production: each record landed as its own
	// variant file, atomically, the moment its group finished — at its
	// last covering invocation's completion for executing groups, in the
	// verification pass for all-served groups, after the retry for
	// drifted subjects. Untouched records — served, outside-policy,
	// departed — need no rewrite, and on the degraded path nothing
	// executed under groups, so nothing installed.
	return tr, nil
}

// roundCandidates selects round N's checkable fingerprints: each
// unproven subject's Nth variant, gated on its licensing exclusions. A
// gate-refused variant counts as round progress — refusal must never
// end the walk while later variants remain, or a poisoned early
// variant would permanently mask a serveable one (spurious re-run,
// forever). A record's observation proves nothing about the surfaces
// its capture-time exclusions elided, so it serves only while the
// current policy still asserts every one of them: a withdrawn
// exclusion re-runs the witnesses it licensed, while an added
// exclusion serves existing evidence unchanged (its identities are in
// the manifest and simply revalidate).
func roundCandidates(subjects []gofresh.Subject, cached map[string][]witnesscache.Record, valid map[gofresh.Subject]bool, round int, current []string, refused func(gofresh.Subject)) (map[gofresh.Subject]gofresh.Fingerprint, bool) {
	fps := map[gofresh.Subject]gofresh.Fingerprint{}
	advanced := false
	for _, s := range subjects {
		vars := cached[s.Package+"."+s.Symbol]
		if valid[s] || round >= len(vars) {
			continue
		}
		advanced = true
		if !exclusionsStillAsserted(vars[round].ObservationExclusions, current) {
			refused(s)
			continue
		}
		fps[s] = vars[round].Fingerprint.ToGofresh()
	}
	return fps, advanced
}

// exclusionsStillAsserted reports whether every capture-time exclusion
// is still in the group's current canonical set — the serving condition
// for evidence whose manifest elided those identities. Both sides are
// canonical (sorted, deduplicated), so the check is a linear merge.
func exclusionsStillAsserted(recorded, current []string) bool {
	i := 0
	for _, want := range recorded {
		for i < len(current) && current[i] < want {
			i++
		}
		if i >= len(current) || current[i] != want {
			return false
		}
	}
	return true
}

// prepareWitnessGroups builds each capture group's serving state: the
// analysis view over its in-policy subjects, the pre-execution
// fingerprint check partitioning served from stale, the stale captures,
// and the observation-proof leg for packages executing exactly one
// top-level runnable. Any fault returns the degraded reason: the caller
// serves nothing and executes everything covered
// (REQ-evidence-freshness-degrade).
func prepareWitnessGroups(ctx context.Context, dir string, pc *policyCapture, cached map[string][]witnesscache.Record) ([]*witnessGroup, string) {
	var out []*witnessGroup
	for _, g := range pc.groups {
		subjects := groupSubjects(g, pc.globalCount)
		if len(subjects) == 0 {
			continue
		}
		engine, err := groupEngine(ctx, dir, g)
		if err != nil {
			return nil, err.Error()
		}
		view, err := engine.NewView(ctx, subjects, dir)
		if err != nil {
			return nil, err.Error()
		}
		wg := &witnessGroup{
			g: g, engine: engine, view: view,
			recorded:    map[gofresh.Subject]witnesscache.Record{},
			executedWhy: map[gofresh.Subject]string{},
			stale:       map[string][]string{},
			fps:         map[gofresh.Subject]gofresh.Fingerprint{},
			refreshed:   map[gofresh.Subject]bool{},
		}
		// Round-based variant checking: round N checks each unproven
		// subject's Nth variant, and the first variant proving equivalent
		// serves — deterministic by digest-sorted load order. Variants
		// differing only in manifests or proof attachment can both prove
		// equivalent; each is a proven equivalence, so either serves
		// soundly. Rounds cost only fingerprint checks, never analysis or
		// execution.
		valid := map[gofresh.Subject]bool{}
		for round := 0; ; round++ {
			fps, advanced := roundCandidates(subjects, cached, valid, round, g.excludedPaths, func(s gofresh.Subject) {
				wg.executedWhy[s] = "recorded under a withdrawn observation exclusion"
			})
			if !advanced {
				break
			}
			if len(fps) == 0 {
				// Every variant this round was gate-refused; later
				// variants may still prove equivalent.
				continue
			}
			verdicts, err := checkFingerprints(ctx, view, fps)
			if err != nil {
				return nil, err.Error()
			}
			grown := map[gofresh.Subject]witnesscache.Record{}
			refreshed := map[gofresh.Subject]gofresh.Fingerprint{}
			for s := range fps {
				if verdicts[s].Status == gofresh.Valid {
					valid[s] = true
					wg.recorded[s] = cached[s.Package+"."+s.Symbol][round]
					delete(wg.executedWhy, s)
					continue
				}
				if rec, fp, ok := compartmentGrownRefresh(ctx, view, cached[s.Package+"."+s.Symbol][round], verdicts[s], s); ok {
					// Exactly stale "test variants" with an inert
					// recorded-to-current ledger delta: the movement is
					// additions no unchanged declaration can observe, so the
					// record still proves equivalence
					// (REQ-evidence-witness-freshness's inert-growth
					// carve-out). The refreshed fingerprints re-check as ONE
					// batch below — per-record checking multiplied full
					// check windows by the carve-out count — and an accepted
					// record rides refreshed to the current compartment, so
					// the post-run revalidation and every later run read it
					// plainly valid.
					grown[s] = rec
					refreshed[s] = fp
					continue
				}
				// The last-checked variant's refusal explains the coming
				// re-execution; a later round's success deletes it.
				wg.executedWhy[s] = verdicts[s].Reason
			}
			if len(refreshed) != 0 {
				grownVerdicts, err := checkFingerprints(ctx, view, refreshed)
				for s, rec := range grown {
					// A faulting re-check leaves the subject on its
					// original stale reason and it simply executes — the
					// carve-out degrades per record, never the run
					// (REQ-evidence-freshness-degrade). A REFUSING
					// re-check names the true mover the compartment
					// verdict hid (the spec's stale-variant attribution:
					// the verdict with its moved inputs), never the
					// compartment reason that concealed it.
					if err == nil && grownVerdicts[s].Status == gofresh.Valid {
						valid[s] = true
						wg.recorded[s] = rec
						wg.refreshed[s] = true
						delete(wg.executedWhy, s)
						continue
					}
					wg.executedWhy[s] = verdicts[s].Reason
					if err == nil && grownVerdicts[s].Reason != "" {
						wg.executedWhy[s] = grownVerdicts[s].Reason
					}
				}
				if err != nil && ctx.Err() != nil {
					return nil, err.Error()
				}
			}
		}
		for _, s := range subjects {
			if valid[s] {
				// Proven equivalent: the chosen variant serves, pending
				// post-run revalidation.
				wg.served = append(wg.served, s)
				continue
			}
			// Anything short of valid — stale, unverifiable, absent —
			// executes; absence of proof never serves an outcome. A
			// subject that fails to capture simply stays unpublishable;
			// its execution and evidence are untouched.
			wg.stale[s.Package] = append(wg.stale[s.Package], s.Symbol)
			if fp, err := view.Capture(ctx, s); err == nil {
				wg.fps[s] = fp
			}
		}
		for pkg, names := range wg.stale {
			if len(names) != 1 {
				continue
			}
			s := gofresh.Subject{Package: pkg, Symbol: names[0]}
			if fp, ok := wg.fps[s]; ok && fp.PurityAssertion == "" {
				wg.candidates = append(wg.candidates, s)
			}
		}
		sort.Slice(wg.candidates, func(i, j int) bool {
			a, b := wg.candidates[i], wg.candidates[j]
			if a.Package != b.Package {
				return a.Package < b.Package
			}
			return a.Symbol < b.Symbol
		})
		wg.observed, wg.observedFPs = observedView(ctx, wg.view, wg.candidates)
		out = append(out, wg)
	}
	return out, ""
}

// compartmentGrownRefresh applies REQ-evidence-witness-freshness's
// inert-growth carve-out to one refused variant, up to the re-check. A
// verdict of exactly stale "test variants" certifies only the subject's
// core source closure — gofresh orders the compartment comparison after
// the core and before the environment tiers, so a moved guard or runtime
// input can hide behind that reason — and these fingerprints never carry
// a refinement. The carve-out therefore completes the proof itself: the
// record's persisted compartment ledger must diff inert against the
// current view's (the only movement is additions no unchanged declaration
// can observe), and the recorded fingerprint refreshed to the current
// compartment hash must check plainly valid against the current view,
// which enforces every remaining pin exactly as an ordinary serve — the
// caller batches that check across every candidate of the round. The
// returned record carries the refreshed fingerprint and the current
// ledger; it serves only when the batched check answers valid. Any fault
// refuses — the subject just executes (REQ-evidence-freshness-degrade).
// The nil-ledger guard is defense in depth: Load already refuses
// ledgerless records.
func compartmentGrownRefresh(ctx context.Context, view *gofresh.View, rec witnesscache.Record, verdict gofresh.Verdict, s gofresh.Subject) (witnesscache.Record, gofresh.Fingerprint, bool) {
	if verdict.Status != gofresh.Stale || verdict.Reason != "test variants" || rec.CompartmentLedger == nil {
		return witnesscache.Record{}, gofresh.Fingerprint{}, false
	}
	current, err := view.TestVariantLedger(s)
	if err != nil {
		return witnesscache.Record{}, gofresh.Fingerprint{}, false
	}
	if !gofresh.DiffTestVariantLedgers(rec.CompartmentLedger.ToGofresh(), current).Inert() {
		return witnesscache.Record{}, gofresh.Fingerprint{}, false
	}
	fp, err := view.Capture(ctx, s)
	if err != nil || fp.TestVariantClosure == "" {
		return witnesscache.Record{}, gofresh.Fingerprint{}, false
	}
	refreshed := rec.Fingerprint.ToGofresh()
	refreshed.TestVariantClosure = fp.TestVariantClosure
	rec.Fingerprint = witnesscache.FromGofresh(refreshed)
	rec.CompartmentLedger = witnesscache.LedgerFromGofresh(current)
	return rec, refreshed, true
}

// execMerge folds one or more selective executions into the per-process
// facts evidence and publication key on: attributed rows, diagnostics,
// each launched process's terminal disposition, and each process's owned
// observation.
type execMerge struct {
	rows  []*stipulatorv1.TestResult
	diags []*stipulatorv1.FailureDiagnostic
	disp  map[producerKey]stipulatorv1.HealthDisposition
	obs   map[producerKey]*ProcessObservation
}

func newExecMerge() *execMerge {
	return &execMerge{
		disp: map[producerKey]stipulatorv1.HealthDisposition{},
		obs:  map[producerKey]*ProcessObservation{},
	}
}

func (m *execMerge) add(res *SelectionResult) {
	m.rows = append(m.rows, res.Tests...)
	m.diags = append(m.diags, res.Diagnostics...)
	for _, p := range res.Processes {
		if p.Producer != nil {
			m.disp[keyOfProducer(p.Producer)] = p.Disposition
		}
	}
	for _, o := range res.Observations {
		m.obs[keyOfProducer(o.Wire.GetProducer())] = o
	}
}

// executeSelections runs the invocations serially; onCompleted, when
// non-nil, fires after each invocation's outcomes merge - the hook
// that lets a witness group finish and install the moment its last
// covering invocation completes, so a run dying mid-execution keeps
// every record already produced (REQ-evidence-witness-cache-format's
// completed-group durability).
func executeSelections(ctx context.Context, p *stipulatorv1.TestPolicy, normalized map[string]*NormalizedInvocation, staleSel map[string]TestSelection, m *execMerge, onCompleted func(invocation string) error) error {
	for _, inv := range p.GetInvocations() {
		sel := staleSel[inv.GetName()]
		if len(sel) == 0 {
			continue
		}
		res, err := ExecuteSelection(ctx, normalized[inv.GetName()], sel)
		if err != nil {
			return err
		}
		m.add(res)
		if onCompleted != nil {
			if err := onCompleted(inv.GetName()); err != nil {
				return err
			}
		}
	}
	return nil
}

// finishGroup revalidates one group's served records against the
// post-execution tree and publishes its executed subjects' new records.
// A served record that no longer checks valid drifted mid-run: its
// served outcome is discarded and the subject joins the drifted set for
// the run's single retry. A view that no longer describes the tree — or
// a fingerprint check that faults — voids every equivalence proof of the
// group the same way, and nothing executed under it can publish; the
// executed evidence itself stands untouched. The error return is
// reserved for caller cancellation.
func finishGroup(ctx context.Context, wg *witnessGroup, m *execMerge) ([]gofresh.Subject, []witnesscache.Record, map[gofresh.Subject]string, error) {
	// The re-check runs first and the validation follows as the deferred
	// close: under the deferred-close engine every check's verdict is
	// provisional until the view validates, so validation must be the
	// LAST view operation before a served outcome stands — a change
	// persisting to it discards the provisional verdicts with it.
	servedFPs := map[gofresh.Subject]gofresh.Fingerprint{}
	for _, s := range wg.served {
		servedFPs[s] = wg.recorded[s].Fingerprint.ToGofresh()
	}
	verdicts, err := checkFingerprints(ctx, wg.view, servedFPs)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		reasons := map[gofresh.Subject]string{}
		for pkg, names := range wg.stale {
			for _, name := range names {
				reasons[gofresh.Subject{Package: pkg, Symbol: name}] = "post-run served-record revalidation faulted: " + err.Error()
			}
		}
		// The discarded serves re-execute holding prior evidence, so each
		// names why serving refused it — the same attribution the
		// validation-refusal arm carries.
		for _, s := range wg.served {
			wg.executedWhy[s] = "post-run served-record revalidation faulted: " + err.Error()
		}
		return append([]gofresh.Subject(nil), wg.served...), nil, reasons, nil
	}
	if err := wg.view.Validate(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		// Every discarded serve re-executes holding prior evidence, so
		// each names why serving refused it (the spec's attribution for
		// re-executed record holders), and the executed-but-unpublishable
		// stale set carries the same cause on the uncacheable ledger.
		reasons := map[gofresh.Subject]string{}
		for pkg, names := range wg.stale {
			for _, name := range names {
				reasons[gofresh.Subject{Package: pkg, Symbol: name}] = "source producer validation failed: " + err.Error()
			}
		}
		for _, s := range wg.served {
			wg.executedWhy[s] = "source producer validation failed: " + err.Error()
		}
		return append([]gofresh.Subject(nil), wg.served...), nil, reasons, nil
	}
	var drifted []gofresh.Subject
	for _, s := range wg.served {
		if verdicts[s].Status != gofresh.Valid {
			drifted = append(drifted, s)
			// The served record held prior evidence and now re-executes:
			// the drift verdict's reason - movers named - is its
			// executed-reason attribution, never a cold read.
			wg.executedWhy[s] = "mid-run drift: " + verdicts[s].Reason
		}
	}
	records, reasons, err := publishExecuted(ctx, wg, m)
	if err != nil {
		return nil, nil, nil, err
	}
	// A carve-out-served record that survived post-run revalidation
	// installs refreshed — current compartment hash and ledger — so the
	// next run reads it plainly valid instead of re-proving the same
	// inert delta. A drifted one never installs.
	for _, s := range wg.served {
		if wg.refreshed[s] && verdicts[s].Status == gofresh.Valid {
			records = append(records, wg.recorded[s])
		}
	}
	return drifted, records, reasons, nil
}

// subjectRun is one executed subject's cache-eligible material: the
// granting process's owned observation and the subject's outcomes and
// registrations from that process alone (REQ-policy-attribution).
type subjectRun struct {
	obs      *ProcessObservation
	outcomes map[string]string
	regs     []verify.Registration
}

// grantingRun finds the one selective process whose disposition permits
// caching subject's outcome: a process classified HEALTHY — the
// per-process verdict, never a package or invocation health, none exists
// on this path — that produced the subject's top-level terminal event
// and owns a completed observation. A subject whose package process
// disposed red gets its chance from the isolation pass's solo process; a
// subject no healthy process granted stays uncacheable.
func grantingRun(s gofresh.Subject, m *execMerge) (*subjectRun, string) {
	byProducer := map[producerKey][]*stipulatorv1.TestResult{}
	var order []producerKey
	for _, row := range m.rows {
		if row.GetPackage() != s.Package {
			continue
		}
		test := row.GetTest()
		if test != s.Symbol && !strings.HasPrefix(test, s.Symbol+"/") {
			continue
		}
		k := keyOfProducer(row.GetProducer())
		if _, ok := byProducer[k]; !ok {
			order = append(order, k)
		}
		byProducer[k] = append(byProducer[k], row)
	}
	sawUnhealthy, sawUnproven := false, false
	for _, k := range order {
		if m.disp[k] != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
			sawUnhealthy = true
			continue
		}
		obs := m.obs[k]
		if obs == nil || obs.Wire.GetCompleted() == nil {
			// The producing process's testlog flush is unproven: its tests
			// execute and witness, they just cannot cache.
			sawUnproven = true
			continue
		}
		sr := &subjectRun{obs: obs, outcomes: map[string]string{}}
		contradicted := false
		for _, row := range byProducer[k] {
			var word string
			switch row.GetOutcome() {
			case stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED:
				word = "passed"
			case stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED:
				word = "skipped"
			default:
				// A failed result inside a healthy process is a
				// contradiction; refuse the record rather than cache
				// either side of it.
				contradicted = true
			}
			sr.outcomes[row.GetPackage()+"."+row.GetTest()] = word
			for _, req := range row.GetRegistrations() {
				sr.regs = append(sr.regs, verify.Registration{Package: s.Package, Test: row.GetTest(), Requirement: req})
			}
		}
		if contradicted || sr.outcomes[s.Package+"."+s.Symbol] == "" {
			sawUnhealthy = sawUnhealthy || contradicted
			continue
		}
		return sr, ""
	}
	switch {
	case sawUnproven:
		return nil, "producing process's testlog flush unproven"
	case sawUnhealthy:
		return nil, "no healthy process granted the outcome"
	default:
		return nil, "no process produced the subject's terminal event"
	}
}

// publishExecuted assembles the cache records one group's executed
// subjects support, reusing the producer-validation ladder: per-process
// eligibility, the observation-proof leg where every candidate of the
// group can attach, plain per-process manifests otherwise, and a
// post-run fingerprint check per record — a stale verdict is mid-run
// drift of the record's inputs, dropped so the next run re-derives it.
// The error return is reserved for caller cancellation. The second
// return names, per unpublished subject, the leg that refused
// (REQ-evidence-witness-freshness's diagnosable-set requirement).
func publishExecuted(ctx context.Context, wg *witnessGroup, m *execMerge) ([]witnesscache.Record, map[gofresh.Subject]string, error) {
	var order []gofresh.Subject
	for pkg, names := range wg.stale {
		for _, name := range names {
			order = append(order, gofresh.Subject{Package: pkg, Symbol: name})
		}
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		return a.Symbol < b.Symbol
	})
	eligible := map[gofresh.Subject]*subjectRun{}
	reasons := map[gofresh.Subject]string{}
	for _, s := range order {
		if _, ok := wg.fps[s]; !ok {
			reasons[s] = "pre-execution fingerprint capture failed"
			continue
		}
		sr, why := grantingRun(s, m)
		if sr == nil {
			reasons[s] = why
			continue
		}
		eligible[s] = sr
	}

	// Observation-completeness proofs attach only when every candidate of
	// the group can attach: the observed view revalidates as one unit, so
	// a single candidate whose process left no completed observation
	// drops the proof leg whole and every candidate falls back to the
	// plain per-process manifest.
	attached := map[gofresh.Subject]gofresh.Fingerprint{}
	attachedValid := map[gofresh.Subject]bool{}
	if wg.observed != nil && len(wg.observedFPs) == len(wg.candidates) {
		complete := true
		for _, s := range wg.candidates {
			sr, ok := eligible[s]
			if !ok {
				complete = false
				break
			}
			fp, err := wg.observed.AttachObservation(s, wg.observedFPs[s], sr.obs.Runtime)
			if err != nil {
				complete = false
				break
			}
			state, err := runtimeinput.CompletedState(sr.obs.Runtime)
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
		if complete && len(wg.candidates) > 0 {
			if err := wg.observed.Validate(ctx); err != nil {
				if ctx.Err() != nil {
					return nil, nil, ctx.Err()
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
		sr := eligible[s]
		if sr == nil {
			continue
		}
		if fp, ok := attached[s]; ok {
			final[s] = fp
			continue
		}
		fp := wg.fps[s]
		if fp.ObservationAssertion == "" {
			state, err := runtimeinput.CompletedState(sr.obs.Runtime)
			if err != nil {
				continue
			}
			fp.RuntimeInputs, fp.RuntimeDigest = state.Manifest, state.Digest
		}
		final[s] = fp
	}

	verdicts := map[gofresh.Subject]gofresh.Verdict{}
	unvalidated := map[gofresh.Subject]gofresh.Fingerprint{}
	for s, fp := range final {
		if attachedValid[s] {
			verdicts[s] = gofresh.Verdict{Status: gofresh.Valid}
		} else {
			unvalidated[s] = fp
		}
	}
	checked, err := checkFingerprints(ctx, wg.view, unvalidated)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		// A faulting post-run check publishes nothing for the group; the
		// executed evidence stands and every subject counts uncacheable.
		for _, s := range order {
			if _, ok := reasons[s]; !ok {
				reasons[s] = "post-run producer validation faulted: " + err.Error()
			}
		}
		return nil, reasons, nil
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
	// The deferred close: the view's validation is the one closing
	// observation for the post-run checks above — no record publishes
	// unless the tree still agrees, and a refusal here discards the
	// provisional verdicts exactly as gofresh's deferred-close contract
	// requires. The executed evidence itself stands untouched. With
	// nothing checked and nothing publishable there is no window to
	// close and no record to gate: the fully-warm pass pays no
	// observation here.
	if len(final) != 0 {
		if err := wg.view.Validate(ctx); err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			for _, s := range order {
				if _, ok := reasons[s]; !ok {
					reasons[s] = "post-run producer validation failed: " + err.Error()
				}
			}
			return nil, reasons, nil
		}
	}

	var records []witnesscache.Record
	for _, s := range order {
		fp, ok := final[s]
		if !ok || verdicts[s].Status != gofresh.Valid {
			continue
		}
		sr := eligible[s]
		rec, ok := assembleWitnessRecord(wg.view, s, fp, sr.outcomes, sr.regs, wg.g.excludedPaths)
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
	return records, reasons, nil
}

// retryDrifted re-executes each drifted served subject exactly once,
// within the same run, under its covering invocation's envelope: the
// served outcome was already discarded, so the retry's execution is the
// subject's outcome for this run. Fresh fingerprints are captured against
// the current tree before the retry executes; a retry whose record still
// fails validation afterwards — still drifting — is dropped and counted
// uncacheable, never retried again.
func retryDrifted(ctx context.Context, dir string, p *stipulatorv1.TestPolicy, normalized map[string]*NormalizedInvocation, covering map[string]*NormalizedInvocation, driftedByGroup map[*witnessGroup][]gofresh.Subject, m *execMerge) ([]witnesscache.Record, map[gofresh.Subject]string, error) {
	// Fresh pre-retry capture per group: the old view described a tree the
	// drift already left behind.
	type retryState struct {
		wg          *witnessGroup
		view        *gofresh.View
		fps         map[gofresh.Subject]gofresh.Fingerprint
		candidates  []gofresh.Subject
		observed    *gofresh.View
		observedFPs map[gofresh.Subject]gofresh.Fingerprint
	}
	var states []retryState
	retrySel := map[string]TestSelection{}
	for wg, subjects := range driftedByGroup {
		if len(subjects) == 0 {
			continue
		}
		sort.Slice(subjects, func(i, j int) bool {
			a, b := subjects[i], subjects[j]
			if a.Package != b.Package {
				return a.Package < b.Package
			}
			return a.Symbol < b.Symbol
		})
		for _, s := range subjects {
			n := covering[s.Package]
			sel := retrySel[n.Name]
			if sel == nil {
				sel = TestSelection{}
				retrySel[n.Name] = sel
			}
			sel[s.Package] = append(sel[s.Package], s.Symbol)
		}
		st := retryState{wg: wg, fps: map[gofresh.Subject]gofresh.Fingerprint{}}
		// A failed view or capture leaves the retry unpublishable — it
		// still executes, its outcome still witnesses, its record is
		// simply dropped and counted uncacheable.
		if view, err := wg.engine.NewView(ctx, subjects, dir); err == nil {
			st.view = view
			for _, s := range subjects {
				if fp, err := view.Capture(ctx, s); err == nil {
					st.fps[s] = fp
				}
			}
		} else if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		// The retry's proof candidates follow the same per-process solo
		// rule as the main pass, over the retry's own stale set: a retried
		// subject alone in its package runs in a process of its own.
		perPkg := map[string]int{}
		for _, s := range subjects {
			perPkg[s.Package]++
		}
		for _, s := range subjects {
			if fp, ok := st.fps[s]; ok && perPkg[s.Package] == 1 && fp.PurityAssertion == "" {
				st.candidates = append(st.candidates, s)
			}
		}
		st.observed, st.observedFPs = observedView(ctx, st.view, st.candidates)
		states = append(states, st)
	}
	if err := executeSelections(ctx, p, normalized, retrySel, m, nil); err != nil {
		return nil, nil, err
	}
	var published []witnesscache.Record
	reasons := map[gofresh.Subject]string{}
	for _, st := range states {
		if st.view == nil {
			continue
		}
		if err := st.view.Validate(ctx); err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			for rs := range st.fps {
				reasons[rs] = "retry producer validation failed: " + err.Error()
			}
			continue
		}
		stale := map[string][]string{}
		for s := range st.fps {
			stale[s.Package] = append(stale[s.Package], s.Symbol)
		}
		// The retry publishes through the same ladder over a synthetic
		// group state, its proof leg captured before the retry executed.
		rwg := &witnessGroup{
			g: st.wg.g, engine: st.wg.engine, view: st.view, stale: stale, fps: st.fps,
			candidates: st.candidates, observed: st.observed, observedFPs: st.observedFPs,
		}
		records, retryReasons, err := publishExecuted(ctx, rwg, m)
		if err != nil {
			return nil, nil, err
		}
		// The retry is the subject's one re-derivation this run; a record
		// still refusing keeps the retry's reason.
		maps.Copy(reasons, retryReasons)
		published = append(published, records...)
	}
	return published, reasons, nil
}

// consumeMerge folds one merged selective execution into the run's
// witness-evidence view: failed and skipped results are recorded
// regardless — red is a fact whatever produced it — while a pass grants
// an outcome only from a process whose own disposition is healthy, so a
// completed pass inside a red process reads unwitnessed unless its
// isolation re-run granted it solo (REQ-evidence-witness-freshness's
// isolation sentence). When one test name carries several results the
// worst outcome wins, so a single red occurrence is never papered over
// by a green sibling.
// consumeMergeFailuresOnly folds one merge's failed outcomes, its
// diagnostics, and every row's registrations into the run — pass and
// skip outcomes are stripped, never the rows: the source executions lack
// race rigor, so they can indict evidence but never grant it, while
// their registrations and execution remain facts the
// unbacked-registration cross-check and the executed counts must see.
func consumeMergeFailuresOnly(tr *verify.TestRun, m *execMerge, ranTop map[string]bool) {
	filtered := &execMerge{disp: m.disp, obs: m.obs, diags: m.diags}
	for _, row := range m.rows {
		if row.GetOutcome() == stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
			filtered.rows = append(filtered.rows, row)
			continue
		}
		stripped := &stipulatorv1.TestResult{}
		stripped.SetPackage(row.GetPackage())
		stripped.SetTest(row.GetTest())
		stripped.SetRegistrations(row.GetRegistrations())
		stripped.SetProducer(row.GetProducer())
		filtered.rows = append(filtered.rows, stripped)
	}
	// Stripped passes carry no outcome, so the tier maps see nothing:
	// an ineligible leg can neither grant nor class a witness.
	consumeMerge(tr, filtered, ranTop, nil, map[string]bool{}, map[string]bool{})
}

func consumeMerge(tr *verify.TestRun, m *execMerge, ranTop map[string]bool, plainInv, raceGranted, plainGranted map[string]bool) {
	tr.Diagnostics = append(tr.Diagnostics, m.diags...)
	rank := func(o verify.TestOutcome) int {
		switch o {
		case verify.TestFailed:
			return 3
		case verify.TestPassed:
			return 2
		case verify.TestSkipped:
			return 1
		}
		return 0
	}
	for _, row := range m.rows {
		pkg, test := row.GetPackage(), row.GetTest()
		key := pkg + "." + test
		outcome := verify.TestNotRun
		switch row.GetOutcome() {
		case stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED:
			outcome = verify.TestFailed
		case stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED:
			outcome = verify.TestSkipped
		case stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED:
			if m.disp[keyOfProducer(row.GetProducer())] == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
				outcome = verify.TestPassed
				if plainInv[row.GetProducer().GetInvocation()] {
					plainGranted[key] = true
				} else {
					raceGranted[key] = true
				}
			}
		}
		if outcome != verify.TestNotRun && rank(outcome) > rank(tr.Outcomes[key]) {
			tr.Outcomes[key] = outcome
		}
		for _, req := range row.GetRegistrations() {
			tr.Registrations = append(tr.Registrations, verify.Registration{
				Package: pkg, Test: test, Requirement: req,
			})
		}
		ranTop[pkg+"."+topLevel(test)] = true
	}
	for _, d := range m.diags {
		if d.GetTest() == "" {
			// A diagnostic no single test owns — an envelope cutoff, a
			// package abort, a build failure — is the visibility story of
			// the subjects it denied: it rides the result as its typed
			// row alone (one fact, one home).
			continue
		}
		if tr.Failures == nil {
			tr.Failures = map[string]string{}
		}
		key := d.GetPackage() + "." + d.GetTest()
		if prev, ok := tr.Failures[key]; ok {
			tr.Failures[key] = prev + "\n" + d.GetOutput()
		} else {
			tr.Failures[key] = d.GetOutput()
		}
	}
}
