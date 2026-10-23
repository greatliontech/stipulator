package golang

import (
	"context"
	"fmt"
	"maps"
	"runtime/debug"
	"slices"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
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
// (REQ-policy-conservation's visibility principle). A package several
// eligible invocations select holds one record per capture group — the
// record identity carries the group coordinate — so each group serves
// or executes its own leg and the evidence merge takes the worst
// outcome across legs; ineligible legs still execute every run,
// contributing failures and registrations only. Visibility
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
func RunWitnesses(ctx context.Context, dir string, seeding verify.WitnessSeeding) (*verify.TestRun, error) {
	pc, err := LoadCapture(ctx, dir)
	if err != nil {
		return nil, err
	}
	return runWitnesses(ctx, pc, nil, seeding)
}

// RunWitnessesPolicy is RunWitnesses over the operation's capture of
// its already-loaded accepted policy — the unified check captures once
// for its own notices and short-circuits and hands the capture through.
func RunWitnessesPolicy(ctx context.Context, pc *Capture, seeding verify.WitnessSeeding) (*verify.TestRun, error) {
	return runWitnesses(ctx, pc, nil, seeding)
}

// RunWitnessesScoped is the witness-only selective execution narrowed
// further to a caller-named subject scope: fresh records still serve
// for the whole tree, and only stale subjects inside the scope execute
// - the remainder is recorded scope-skipped, never broken. The degraded
// path expands to the scope's own full execution, never the tree's
// (REQ-check-verdict-scoped).
func RunWitnessesScoped(ctx context.Context, pc *Capture, scope map[gofresh.Subject]bool, seeding verify.WitnessSeeding) (*verify.TestRun, error) {
	return runWitnesses(ctx, pc, scope, seeding)
}

// witnessGroup is one capture group's serving state: the analysis views
// over its in-policy subjects, the cached records found for them, and the
// served/stale partition the pre-execution fingerprint check produced.
type witnessGroup struct {
	g *captureGroup
	// view is the group's one analysis view: the expensive observation,
	// paid once per group, whose facts every package's leg shares;
	// released once every leg has.
	view *witnessSnapshot
	// legs is the publication state per PACKAGE — the unit of
	// persistence (REQ-policy-cancellation-unit): the package's serving
	// checks and captures start on its own sibling of view. Publication
	// and serving close independently over shared facts; the leg releases
	// once the package has published and, where it serves, revalidated.
	legs map[string]*packageLeg
	// executedWhy names, per stale subject that held prior evidence, why
	// its variants failed to serve - the last-checked variant's verdict
	// reason, movers named by gofresh's attribution.
	executedWhy map[gofresh.Subject]string
	// served are the subjects whose recorded fingerprint checked valid
	// before execution; stale maps each package to its executing top-level
	// names, sorted.
	served []gofresh.Subject
	stale  map[string][]string
}

// packages lists the group's packages in order: every package holding a
// served or a stale subject.
func (wg *witnessGroup) packages() []string {
	seen := map[string]bool{}
	var out []string
	for pkg := range wg.stale {
		if !seen[pkg] {
			seen[pkg] = true
			out = append(out, pkg)
		}
	}
	for _, s := range wg.served {
		if !seen[s.Package] {
			seen[s.Package] = true
			out = append(out, s.Package)
		}
	}
	sort.Strings(out)
	return out
}

// servedIn is the group's served subjects of one package.
func (wg *witnessGroup) servedIn(pkg string) []gofresh.Subject {
	var out []gofresh.Subject
	for _, s := range wg.served {
		if s.Package == pkg {
			out = append(out, s)
		}
	}
	return out
}

func (wg *witnessGroup) selectedRecord(s gofresh.Subject) witnesscache.Record {
	return wg.legs[s.Package].chosen[s].record
}

func runWitnesses(ctx context.Context, pc *Capture, scope map[gofresh.Subject]bool, seeding verify.WitnessSeeding) (*verify.TestRun, error) {
	if err := pc.ConfigureExecution(false, scope); err != nil {
		return nil, err
	}
	rep := progress.FromContext(ctx)
	rep.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	dir := pc.dir
	d, err := pc.discover(ctx)
	if err != nil {
		return nil, err
	}
	// Random-seeded witnesses are resolved before any serving decision:
	// they execute every run (REQ-evidence-witness-freshness-seeded). A
	// classification fault degrades serving whole — every in-policy
	// subject executes and nothing publishes — the fail-closed
	// direction, exactly as an engine fault degrades
	// (REQ-evidence-freshness-degrade).
	seedingErr := classifySeeded(d, seeding)
	if seedingErr != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// The classification was the run's last question to its backend: a
	// backend that can release what answering cost it does so now —
	// before the universe's listings, the engines' loads and the first
	// process spawn (REQ-evidence-resolution-freshness-quiesce).
	quiesceSeeding(seeding)
	// A universe fault is a freshness-path fault, not a selection fault:
	// selection needs only the policy's own discovery, and the universe
	// feeds the outside-policy accounting the degraded reason then names.
	// It degrades exactly as an engine or view fault does
	// (REQ-evidence-freshness-degrade) — capture and discovery faults,
	// by contrast, error, because without them nothing can execute.
	universe, universeErr := pc.ObligationUniverse(ctx)
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
	normalized := pc.byName()
	for _, ic := range d.invocations {
		addExpected(ic.obligations)
	}

	// A within-group double selection (two same-environment invocations
	// naming one package) has no single producing leg, so its subjects
	// never serve or publish — but they are eligible-covered: they
	// execute every run under each of the group's covering invocations,
	// granting outcomes through the ordinary merge.
	ambiguousSel := map[string]TestSelection{}
	ambiguousSubjects := map[gofresh.Subject]bool{}
	for _, ic := range d.invocations {
		g := d.invGroup[ic.n.Name]
		if g == nil {
			continue
		}
		for _, o := range ic.obligations {
			if o.Kind != ObligationTest && o.Kind != ObligationFuzz {
				continue
			}
			// Every test or fuzz obligation of a grouped invocation has
			// its package entry: a group is recorded for witness-eligible
			// invocations alone, and discovery builds their entries from
			// this same obligation list.
			if !g.packages[o.Package].ambiguous {
				continue
			}
			s := gofresh.Subject{Package: o.Package, Symbol: o.Name}
			ambiguousSubjects[s] = true
			sel := ambiguousSel[ic.n.Name]
			if sel == nil {
				sel = TestSelection{}
				ambiguousSel[ic.n.Name] = sel
			}
			sel[o.Package] = append(sel[o.Package], o.Name)
		}
	}
	// Ineligible legs of packages other invocations also select: their
	// failures are evidence the health-judged form would surface, so
	// they execute every run — failures and registrations only, never a
	// grant — an unadmitted non-race pass never grants witness
	// evidence. A caller-named scope narrows them exactly as it narrows
	// every other every-run selection: an out-of-scope leg is left
	// unexecuted, so a red it would have observed never reaches the
	// scoped verdict (REQ-check-verdict-scoped).
	multiIneligible := map[string]TestSelection{}
	for _, ic := range d.invocations {
		if ic.n.WitnessEligible() {
			continue
		}
		for _, o := range ic.obligations {
			if !pc.execution.selectsWitness(ic.n, d.globalCount, o) {
				continue
			}
			sel := multiIneligible[ic.n.Name]
			if sel == nil {
				sel = TestSelection{}
				multiIneligible[ic.n.Name] = sel
			}
			sel[o.Package] = append(sel[o.Package], o.Name)
		}
	}
	// Outside the witness-eligible selection means no eligible coverage
	// anywhere: a subject whose every covering invocation is ineligible
	// still executes (failures count), but its legs can never grant a
	// witness outcome, so it is outside exactly as an unselected subject
	// is — the one classification the health-judged form answers with,
	// applied here before the run for the count and again after it for
	// the subjects the run granted nothing (REQ-check-witness-selection).
	outsideSubjects := d.classifyWitnesses(universe, nil, nil, nil).outside
	outside := len(outsideSubjects)

	loaded := witnesscache.Load(ctx, dir)
	cached := loaded.Records
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// One identity may hold several tree-state variants; at most one can
	// prove equivalent against the current tree, so serving tries each.
	// Identity is group-scoped: a group looks up only records its own
	// producer environment published.
	cachedByKey := map[string][]witnesscache.Record{}
	for _, rec := range cached {
		cachedByKey[rec.IdentityKey()] = append(cachedByKey[rec.IdentityKey()], rec)
	}
	// passedBefore indexes every outcome key any prior record holds as
	// passed — any group, any variant: a failure on such a key flips
	// prior witness state, and its diagnostic carries the runner's
	// execution-environment report (REQ-evidence-flip-environment).
	passedBefore := map[string]bool{}
	for _, rec := range cached {
		for key, out := range rec.Outcomes {
			if out == "passed" {
				passedBefore[key] = true
			}
		}
	}

	var groups []*witnessGroup
	defer func() {
		for _, wg := range groups {
			for _, leg := range wg.legs {
				leg.dispose()
			}
		}
	}()
	degraded := ""
	if universeErr != nil {
		degraded = universeErr.Error()
	} else if seedingErr != nil {
		degraded = seedingErr.Error()
	} else {
		var prepErr error
		groups, degraded, prepErr = prepareWitnessGroups(ctx, dir, d, cachedByKey)
		if prepErr != nil {
			// A toolchain-provenance refusal is a run-level abort, never
			// a degradation (REQ-evidence-toolchain-provenance): the
			// degraded full execution would run over a tree the refused
			// frontend also discovered and selected from.
			return nil, prepErr
		}
		if degraded != "" && ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	// The executing selection per invocation: the stale subjects under
	// their covering invocations — or, degraded, every in-policy subject:
	// serving saves work, it never blocks or weakens witnessing.
	staleSel := map[string]TestSelection{}
	scopeSkipped := map[string]bool{}
	addStale := func(g *captureGroup, s gofresh.Subject) {
		if !pc.execution.permits(s) {
			// A stale subject outside the caller's scope is skipped,
			// recorded so its bindings read scope-skipped rather than
			// broken; on the degraded path this same gate makes the
			// fallback the scope's own full execution.
			scopeSkipped[s.Package+"."+s.Symbol] = true
			return
		}
		// The subject's covering invocation within THIS group: a
		// subject other groups also cover routes each leg to its own
		// group's invocation.
		n := normalized[g.selectingInvocation(s.Package)]
		sel := staleSel[n.Name]
		if sel == nil {
			sel = TestSelection{}
			staleSel[n.Name] = sel
		}
		sel[s.Package] = append(sel[s.Package], s.Symbol)
	}
	if degraded != "" {
		for _, g := range d.groups {
			for _, s := range groupSubjects(g) {
				addStale(g, s)
			}
		}
	} else {
		for _, wg := range groups {
			for pkg, names := range wg.stale {
				for _, name := range names {
					addStale(wg.g, gofresh.Subject{Package: pkg, Symbol: name})
				}
			}
		}
	}
	// Ambiguously covered subjects execute every run, under each of
	// their group's covering invocations, alongside that invocation's
	// stale selection.
	for inv, sel := range ambiguousSel {
		dst := staleSel[inv]
		if dst == nil {
			dst = TestSelection{}
			staleSel[inv] = dst
		}
		for pkg, names := range sel {
			for _, name := range names {
				if !pc.execution.permits(gofresh.Subject{Package: pkg, Symbol: name}) {
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
	uncacheableWhy := map[gofresh.Subject]uncacheable{}
	driftedByGroup := map[*witnessGroup][]gofresh.Subject{}
	// Each install names its unit on the progress stream — the
	// completing invocation, the verification pass's revalidation, the
	// drift retry — so a cancelled run reports what it kept.
	// installNow returns the records that landed: a record the store
	// refused is not cached, and its subject's reason names the store's
	// fault — a filesystem remedy, never the evidence's.
	installNow := func(records []witnesscache.Record) ([]witnesscache.Record, error) {
		return installRecords(ctx, dir, records, uncacheableWhy)
	}
	// One decision line per executing invocation: what executes and the
	// reason most of it serves no record — bounded by the policy, never
	// the test count (REQ-mcp-progress-bound); the per-test attribution rides
	// the result. A subject's reason is its covering group's under THIS
	// invocation: a subject several groups cover carries one reason per
	// leg, each leg's line its own.
	executingInvs := make([]string, 0, len(staleSel))
	for inv := range staleSel {
		executingInvs = append(executingInvs, inv)
	}
	sort.Strings(executingInvs)
	for _, inv := range executingInvs {
		sel := staleSel[inv]
		subjects, reasons := 0, map[string]string{}
		for pkg, names := range sel {
			subjects += len(names)
			for _, wg := range groups {
				if n := normalized[wg.g.selectingInvocation(pkg)]; n == nil || n.Name != inv {
					continue
				}
				for _, name := range names {
					if why, ok := wg.executedWhy[gofresh.Subject{Package: pkg, Symbol: name}]; ok {
						reasons[pkg+"."+name] = executedReason(why)
					}
				}
			}
		}
		line := fmt.Sprintf("executing %s: %s in %s", inv, plural(subjects, "subject"), plural(len(sel), "package"))
		if h := verify.ReasonHistogram(reasons); len(h) > 0 {
			line += fmt.Sprintf(" — %d re-executed: %s", h[0].N, h[0].Why)
		} else if degraded != "" {
			line += " — freshness degraded: " + degraded
		}
		rep.Note(line)
	}
	// The ineligible legs of shared packages execute every run for
	// their failures alone: their line says so, once per leg.
	ineligibleInvs := make([]string, 0, len(multiIneligible))
	for inv := range multiIneligible {
		ineligibleInvs = append(ineligibleInvs, inv)
	}
	sort.Strings(ineligibleInvs)
	for _, inv := range ineligibleInvs {
		sel := multiIneligible[inv]
		subjects := 0
		for _, names := range sel {
			subjects += len(names)
		}
		rep.Note(fmt.Sprintf("executing %s: %s in %s — ineligible leg of shared packages, failures only, never a witness outcome", inv, plural(subjects, "subject"), plural(len(sel), "package")))
	}
	// The unit of persistence is the package: the moment a package's
	// process and its isolation re-runs complete, every group that
	// executes it under this invocation publishes and installs its
	// executed records — while its sibling packages still run, so a run
	// dying mid-invocation keeps every package already finished
	// (REQ-policy-cancellation-unit); its served records wait for the
	// verification pass, after every execution. A stale package the
	// caller's scope left out executes nothing: its subjects publish
	// their reasons in the verification pass.
	onPackageDone := func(inv string, unit packageUnit) error {
		for _, wg := range groups {
			// One package can sit in several groups — two invocations
			// under different build coordinates both selecting it — each
			// group running it under its own invocation; the completing
			// invocation finishes the package in its own group alone, or
			// another group's package would publish under this run's
			// processes.
			p, ok := wg.g.packages[unit.pkg]
			if !ok || p.ambiguous || p.inv != inv || len(wg.stale[unit.pkg]) == 0 {
				continue
			}
			if leg := wg.legs[unit.pkg]; leg == nil || leg.published {
				continue
			}
			records, reasons, err := publishPackage(ctx, wg, unit.pkg, m)
			if err != nil {
				return err
			}
			maps.Copy(uncacheableWhy, reasons)
			landed, installErr := installNow(records)
			published = append(published, landed...)
			if len(landed) > 0 {
				rep.Persisted(inv+" "+unit.pkg, len(landed))
			}
			if installErr != nil {
				return installErr
			}
		}
		return nil
	}
	proofs := processProofs{}
	// One ordinal space spans initial executions, isolation and every retry
	// wave, so PID reuse cannot alias independently owned process accounts.
	ordinal := spawnOrdinals()
	for _, wg := range groups {
		proofs.add(wg.g, wg.legs)
	}
	if err := executeSelections(ctx, pc.normalized, staleSel, m, onPackageDone, proofs, ordinal); err != nil {
		return nil, err
	}
	var ineligibleMerge *execMerge
	if len(multiIneligible) > 0 {
		// Ineligible covering legs of shared packages: their
		// failures are evidence the health-judged form would surface, so
		// they execute here in the execution phase and fold after the
		// main merges — failures and registrations only, never a grant.
		ineligibleMerge = newExecMerge()
		if err := executeSelections(ctx, pc.normalized, multiIneligible, ineligibleMerge, nil, nil, ordinal); err != nil {
			return nil, err
		}
	}

	rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	var servedRecords []witnesscache.Record
	plainServedKey := map[string]bool{}
	retryMerge := newExecMerge()
	heldRetries := &retryResources{}
	defer heldRetries.close()
	if degraded == "" {
		var drifted []gofresh.Subject
		revalidated := 0
		for _, wg := range groups {
			// Every execution of the run has completed: the packages
			// nothing executed publish here, and every package's served
			// records revalidate against the tree the run finished on
			// (REQ-check-witness-selection).
			groupDrifted, records, reasons, err := finishGroup(ctx, wg, m)
			if err != nil {
				return nil, err
			}
			maps.Copy(uncacheableWhy, reasons)
			landed, installErr := installNow(records)
			for _, record := range landed {
				// A transformed serve remains conditional through retry waves;
				// its durable historical variant is not an executed initial grant.
				if !slices.Contains(wg.served, gofresh.Subject{Package: record.Package, Symbol: record.Test}) {
					published = append(published, record)
				}
			}
			driftedByGroup[wg] = append(driftedByGroup[wg], groupDrifted...)
			revalidated += len(landed)
			if installErr != nil {
				if revalidated > 0 {
					rep.Persisted("revalidation", revalidated)
				}
				return nil, installErr
			}
			drifted = append(drifted, groupDrifted...)
			isDrifted := map[gofresh.Subject]bool{}
			for _, s := range groupDrifted {
				isDrifted[s] = true
			}
			wg.served = slices.DeleteFunc(wg.served, func(s gofresh.Subject) bool { return isDrifted[s] })
		}
		if revalidated > 0 {
			rep.Persisted("revalidation", revalidated)
		}
		if len(drifted) > 0 {
			retryPublished, retryReasons, err := settleRetries(ctx, pc, groups, driftedByGroup, retryMerge, heldRetries, installNow, ordinal, scopeSkipped)
			if err != nil {
				return nil, err
			}
			maps.Copy(uncacheableWhy, retryReasons)
			published = append(published, retryPublished...)
		}
		for _, wg := range groups {
			for _, s := range wg.served {
				servedRecords = append(servedRecords, wg.selectedRecord(s))
				if n := normalized[wg.g.selectingInvocation(s.Package)]; n != nil && n.PlainWitness {
					plainServedKey[s.Package+"."+s.Symbol] = true
				}
			}
		}
	}

	// Assemble the run's witness-evidence view: executed outcomes gated on
	// their producing process's disposition — a red process yields no
	// green evidence, and the isolation pass's solo processes carry their
	// own dispositions — then the surviving served records, rank-merged:
	// a subject one group serves and another executes carries both legs.
	// The selective runner is the serving class by identity — the degraded
	// empty-served form included (REQ-gap-resolved-pruned's consumers
	// enforce the mark).
	tr := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}, RaceEnabled: true, PlainWitness: map[string]bool{}, OutsidePolicy: outside, OutsideSubjects: outsideSubjects, ScopeSkipped: scopeSkipped, SelectiveServing: true}
	tr.WitnessCacheLoad = loaded.Account
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
			if scopeSkipped[s.Package+"."+s.Symbol] {
				continue
			}
			why = executedReason(why)
			if tr.ExecutedReasons == nil {
				tr.ExecutedReasons = map[string]string{}
			}
			tr.ExecutedReasons[s.Package+"."+s.Symbol] = why
		}
	}
	if degraded == "" {
		for _, g := range d.groups {
			for _, s := range groupSubjects(g) {
				if !slices.Contains(staleSel[g.selectingInvocation(s.Package)][s.Package], s.Symbol) {
					continue
				}
				key := s.Package + "." + s.Symbol
				if tr.ExecutedReasons[key] != "" {
					continue
				}
				for _, refusal := range loaded.Account.GetRefusals() {
					coordinate := refusal.GetCoordinate()
					if coordinate == nil || coordinate.GetGroup() != g.id || coordinate.GetPackage() != s.Package || coordinate.GetTest() != s.Symbol {
						continue
					}
					why := reasonStoreRefused.with(fmt.Sprintf("load witness record (%s): %s", refusal.GetCause(), refusal.GetDetail()))
					if policyReason, refused := g.neverServes[s]; refused {
						why = policyReason
					}
					if tr.ExecutedReasons == nil {
						tr.ExecutedReasons = map[string]string{}
					}
					tr.ExecutedReasons[key] = why.String()
					break
				}
			}
		}
	}
	// A failure that flips prior passing witness evidence carries the
	// runner's execution-environment report before the merges fold into
	// the run's failure map (REQ-evidence-flip-environment). The
	// enriched set spans all three merges, so a shared-package subject
	// failing in two of them still carries one report per invocation.
	flipEnriched := map[string]bool{}
	enrichFlipDiagnostics(m, normalized, passedBefore, flipEnriched)
	enrichFlipDiagnostics(retryMerge, normalized, passedBefore, flipEnriched)
	// The retry's process dispositions join the first pass's per
	// package, the worse of the two standing: a retry timeout is the
	// cause of what it denied, and a healthy retry over the drifted
	// subjects alone never erases a red first pass that denied the rest
	// — the run's own worst-outcome discipline, applied to causes.
	m.absorb(retryMerge)
	enrichFlipDiagnostics(ineligibleMerge, normalized, passedBefore, flipEnriched)
	ranTop := map[string]bool{}
	consumeMerge(tr, m, ranTop, plainInv, raceGranted, plainGranted)
	if ineligibleMerge != nil {
		consumeMergeFailuresOnly(tr, ineligibleMerge, ranTop)
	}
	tr.Ran = len(ranTop)
	// A served record's outcomes merge by worst-outcome rank, exactly as
	// executed legs merge: a subject one group serves and another group
	// executes holds every leg's evidence, and a served pass never
	// papers over an executed failure (or another group's served
	// failure).
	for _, rec := range servedRecords {
		plainRecord := plainServedKey[rec.Package+"."+rec.Test]
		for key, out := range rec.Outcomes {
			o := outcomeFromString(out)
			if verify.OutcomeRank(o) > verify.OutcomeRank(tr.Outcomes[key]) {
				tr.Outcomes[key] = o
			}
			if plainRecord {
				plainGranted[key] = true
			} else {
				raceGranted[key] = true
			}
		}
		tr.Registrations = append(tr.Registrations, rec.Regs...)
	}
	// Fresh counts served subjects, not served records: a subject two
	// groups serve is one saved test in the same unit Ran counts
	// executed tests.
	servedKey := map[string]bool{}
	for _, rec := range servedRecords {
		servedKey[rec.Package+"."+rec.Test] = true
	}
	tr.Fresh = len(servedKey)
	for key, out := range tr.Outcomes {
		if out == verify.TestPassed && plainGranted[key] && !raceGranted[key] {
			tr.PlainWitness[key] = true
		}
	}
	sortRegs(tr)
	tr.Degraded = degraded
	// Uncached is structural — executed subjects left with no reusable
	// evidence after this run: neither a record published this run nor a
	// record that served this run (a served subject whose ineligible
	// shadow leg also executed holds evidence; counting it would name a
	// gap that does not exist). Every genuine drop path still counts:
	// red or aborted processes, missing observations, failed captures,
	// drifted post-run verdicts, and the degraded path alike. Both
	// counts are subject counts, one unit with Ran.
	publishedKey := map[string]bool{}
	for _, rec := range published {
		publishedKey[rec.Package+"."+rec.Test] = true
	}
	for key := range ranTop {
		if !publishedKey[key] && !servedKey[key] {
			tr.Uncached++
		}
	}
	// Per-test attribution for the uncacheable set: the ladder's own
	// refusal reasons, the degraded path,
	// and a structural fallback for anything a drop path missed
	// (REQ-evidence-witness-freshness-diagnosable's attributable reason).
	if tr.Uncached > 0 || len(uncacheableWhy) > 0 {
		tr.UncacheableReasons = map[string]string{}
		for s, why := range uncacheableWhy {
			tr.UncacheableReasons[s.Package+"."+s.Symbol] = why.String()
		}
		for s := range ambiguousSubjects {
			if _, ok := tr.UncacheableReasons[s.Package+"."+s.Symbol]; !ok {
				tr.UncacheableReasons[s.Package+"."+s.Symbol] = reasonNoProducingLeg.String()
			}
		}
		for key := range ranTop {
			if publishedKey[key] || servedKey[key] {
				continue
			}
			if _, ok := tr.UncacheableReasons[key]; !ok {
				if degraded != "" {
					tr.UncacheableReasons[key] = reasonDegraded.with(degraded).String()
				} else {
					tr.UncacheableReasons[key] = reasonNotPublished.with("").String()
				}
			}
		}
		// A subject holding served or published evidence never reads
		// uncacheable — including one whose shadow leg never produced a
		// row, so the key sits outside the executed set.
		for key := range tr.UncacheableReasons {
			if publishedKey[key] || servedKey[key] {
				delete(tr.UncacheableReasons, key)
			}
		}
	}

	// An eligible-covered subject the run granted nothing — no result,
	// or a result its package's process denied a grant — carries the
	// package's disposition under its invocation as its cause, never the
	// outside class; a scope-skipped subject was left unexecuted by the
	// caller, not by the execution (REQ-check-witness-selection).
	tr.NoOutcome = map[string]string{}
	for key, cause := range d.classifyWitnesses(universe, nil, tr.Outcomes, m.packageCause).noOutcome {
		if !scopeSkipped[key] {
			tr.NoOutcome[key] = cause
		}
	}
	denialReasons := map[string][]string{}
	for denied, reason := range m.denied {
		key := denied.subject.Package + "." + denied.subject.Symbol
		if _, granted := tr.Outcomes[key]; !granted && !scopeSkipped[key] {
			denialReasons[key] = append(denialReasons[key], reason.String())
		}
	}
	for key, reasons := range denialReasons {
		slices.Sort(reasons)
		tr.NoOutcome[key] = strings.Join(slices.Compact(reasons), "; ")
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

// executedReason is a re-executed subject's reason as reported: a
// refused variant's verdict names its moved inputs, and a bare stale
// answer reads as prior evidence stale.
func executedReason(why string) string {
	if why == "" {
		return "prior evidence stale"
	}
	return why
}

// quiesceSeeding releases a seeding backend's resident cost once its
// classification — the witness run's last question before execution —
// is answered; a seeding without the extension holds nothing to
// release.
func quiesceSeeding(seeding verify.WitnessSeeding) {
	if q, ok := seeding.(verify.Quiescer); ok {
		q.Quiesce()
	}
}

// plural renders a count with its noun.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
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
// the manifest and simply revalidate). A record's reviewed scratch
// namespaces serve the same way: a read inside one entered no identity,
// so the record serves only while the current policy still declares
// each — a withdrawn or narrowed namespace re-executes the witnesses it
// licensed, an added one serves existing evidence unchanged.
func roundCandidates(subjects []gofresh.Subject, cached map[string][]witnesscache.Record, chosen map[gofresh.Subject]chosenWitness, round int, declarations recordedDeclarations, refused func(gofresh.Subject, string)) (map[gofresh.Subject]gofresh.Fingerprint, bool) {
	fps := map[gofresh.Subject]gofresh.Fingerprint{}
	advanced := false
	for _, s := range subjects {
		vars := cached[s.Package+"."+s.Symbol]
		if _, selected := chosen[s]; selected || round >= len(vars) {
			continue
		}
		advanced = true
		if why := declarations.refusal(vars[round]); why != "" {
			refused(s, why)
			continue
		}
		fps[s] = vars[round].Fingerprint
	}
	return fps, advanced
}

// The serving rounds' gate refusals, each the re-execution reason
// attributed to the subject whose variant it refused.
const (
	refusedWithdrawnExclusion = "recorded under a withdrawn observation exclusion"
	refusedWithdrawnNamespace = "recorded under a withdrawn scratch namespace"
)

// prepareWitnessGroups builds each group's shared native snapshot and independent
// package checking transactions. Variant decisions are provisional until those
// transactions close after execution. Faults split by classifyFault: a
// toolchain-provenance refusal returns as the error — a run-level
// abort, never a degradation — while any other fault returns the
// degraded reason: the caller serves nothing and executes everything
// covered (REQ-evidence-freshness-degrade).
func prepareWitnessGroups(ctx context.Context, dir string, d *policyDiscovery, cached map[string][]witnesscache.Record) ([]*witnessGroup, string, error) {
	var out []*witnessGroup
	var prepared []*packageLeg
	complete := false
	defer func() {
		if !complete {
			for _, leg := range prepared {
				leg.dispose()
			}
		}
	}()
	for g, subjects := range d.populatedGroups() {
		declarations := declarationsFor(g)
		view, inputs, err := groupSnapshot(ctx, dir, g)
		if err != nil {
			if abort, reason := classifyFault(err); !abort {
				return nil, reason, nil
			}
			return nil, "", err
		}
		wg := &witnessGroup{
			g: g, view: view, legs: map[string]*packageLeg{},
			executedWhy: map[gofresh.Subject]string{},
			stale:       map[string][]string{},
		}
		// Every package binds its actual prospective runtime over the shared
		// snapshot. Its historical checking transaction closes independently
		// of every producing attempt and retains all candidate rounds.
		byPkg := map[string][]gofresh.Subject{}
		for _, s := range subjects {
			byPkg[s.Package] = append(byPkg[s.Package], s)
		}
		pkgs := make([]string, 0, len(byPkg))
		for pkg := range byPkg {
			pkgs = append(pkgs, pkg)
		}
		sort.Strings(pkgs)
		for _, pkg := range pkgs {
			subjects := byPkg[pkg]
			var invocation *NormalizedInvocation
			for _, ic := range d.invocations {
				if ic.n.Name == g.selectingInvocation(pkg) {
					invocation = ic.n
					break
				}
			}
			leg, err := nativePackageLeg(ctx, wg.view, inputs, invocation, pkg)
			if err != nil {
				if abort, reason := classifyFault(err); !abort {
					return nil, reason, nil
				}
				return nil, "", err
			}
			wg.legs[pkg] = leg
			prepared = append(prepared, leg)
			leg.checking, err = leg.binding.BeginCheck(subjects)
			if err != nil {
				return nil, err.Error(), nil
			}
			leg.chosen = map[gofresh.Subject]chosenWitness{}
			serving, groupCached := servingCandidates(g.id, subjects, g.neverServes, cached, wg.executedWhy)
			// Round-based variant checking: round N checks each unproven
			// subject's Nth variant, and the first variant proving equivalent
			// serves — deterministic by load order, most recently installed
			// first, so the first round tries the variant the last state
			// change produced. Variants
			// differing only in manifests or proof attachment can both prove
			// equivalent; each is a proven equivalence, so either serves
			// soundly. Rounds cost only fingerprint checks, never analysis or
			// execution.
			for round := 0; ; round++ {
				fps, advanced := roundCandidates(serving, groupCached, leg.chosen, round, declarations, func(s gofresh.Subject, why string) {
					wg.executedWhy[s] = why
				})
				if !advanced {
					break
				}
				if len(fps) == 0 {
					// Every variant this round was gate-refused; later
					// variants may still prove equivalent.
					continue
				}
				var candidates []gofresh.Candidate
				for _, s := range subjects {
					if fp, ok := fps[s]; ok {
						candidates = append(candidates, gofresh.Candidate{Subject: s, Recorded: fp})
					}
				}
				verdicts, err := leg.checking.CheckBatch(ctx, candidates)
				if err != nil {
					if abort, reason := classifyFault(err); !abort {
						return nil, reason, nil
					}
					return nil, "", err
				}
				for _, checked := range verdicts {
					s := checked.Subject
					if checked.Verdict.Status == gofresh.Valid {
						leg.chosen[s] = chosenWitness{index: checked.Index, record: groupCached[s.Package+"."+s.Symbol][round]}
						delete(wg.executedWhy, s)
						continue
					}
					rec := groupCached[s.Package+"."+s.Symbol][round]
					extended, err := compartmentGrownRefresh(ctx, dir, leg.checking, rec, checked)
					if err == nil && extended.Verdict.Status == gofresh.Valid {
						// The selected applicability is provisional until this
						// same view closes in revalidateServed. Only that path
						// returns the pair for installation.
						leg.chosen[s] = chosenWitness{index: extended.Index, record: rec}
						delete(wg.executedWhy, s)
						continue
					}
					// The last-checked variant's refusal explains the coming
					// re-execution; a later round's success deletes it.
					wg.executedWhy[s] = checked.Verdict.Reason
					if err == nil && extended.Verdict.Reason != "" {
						wg.executedWhy[s] = extended.Verdict.Reason
					}
					if err != nil && ctx.Err() != nil {
						if abort, reason := classifyFault(err); !abort {
							return nil, reason, nil
						}
						return nil, "", err
					}
				}
			}
			for _, s := range subjects {
				if _, valid := leg.chosen[s]; valid {
					// Proven equivalent: the chosen variant serves, pending
					// post-run revalidation.
					wg.served = append(wg.served, s)
					continue
				}
				// Anything short of valid — stale, unverifiable, absent —
				// executes; absence of proof never serves an outcome. A
				// subject that fails to capture simply stays unpublishable;
				// its execution and evidence are untouched. A random-seeded
				// witness publishes nothing by contract, so it is never
				// fingerprinted.
				wg.stale[s.Package] = append(wg.stale[s.Package], s.Symbol)
			}
		}
		out = append(out, wg)
	}
	complete = true
	return out, "", nil
}

// servingCandidates partitions one group's subjects for the serving
// rounds: the subjects whose records may serve, with the group's own
// slice of the store (records this producer environment published,
// addressed by the test key). A subject serving refuses — a
// random-seeded witness, an unclassifiable one — never enters the
// rounds: its prior record, if one exists, is exactly the evidence the
// contract refuses to serve, so it executes, and when a record existed
// the refusal is attributed as its re-execution reason
// (REQ-evidence-witness-freshness-seeded).
func servingCandidates(groupID string, subjects []gofresh.Subject, neverServes map[gofresh.Subject]uncacheable, cached map[string][]witnesscache.Record, executedWhy map[gofresh.Subject]string) ([]gofresh.Subject, map[string][]witnesscache.Record) {
	groupCached := map[string][]witnesscache.Record{}
	var serving []gofresh.Subject
	for _, s := range subjects {
		key := s.Package + "." + s.Symbol
		if why, refused := neverServes[s]; refused {
			if len(cached[groupID+"\x00"+key]) > 0 {
				executedWhy[s] = why.String()
			}
			continue
		}
		serving = append(serving, s)
		groupCached[key] = cached[groupID+"\x00"+key]
	}
	return serving, groupCached
}

// compartmentGrownRefresh adds an explicit native extension slot only for
// compartment staleness with a paired historical ledger. Its answer remains
// provisional until this checking unit closes; no endpoint installs here.
func compartmentGrownRefresh(ctx context.Context, dir string, unit *gofresh.CheckingUnit, rec witnesscache.Record, checked gofresh.ProvisionalCheck) (gofresh.ProvisionalCheck, error) {
	if checked.Verdict.Status != gofresh.Stale || checked.Verdict.Reason != gofresh.ReasonTestVariants {
		return checked, nil
	}
	if rec.CompartmentLedger == nil {
		rec.CompartmentLedger = witnesscache.LoadLedger(dir, rec)
	}
	if rec.CompartmentLedger == nil {
		return checked, nil
	}
	extended, err := unit.ExtendInertBatch(ctx, []gofresh.InertRequest{{Index: checked.Index, PriorLedger: rec.CompartmentLedger.ToGofresh()}})
	if err != nil {
		return checked, err
	}
	return extended[0], nil
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
	// denied suppresses only a retry attempt's passing witness grants. Raw
	// rows, registrations and actual process dispositions remain untouched.
	denied map[producerSubject]uncacheable
	// pkgDisp records each package-scoped process's disposition by
	// invocation and package — the execution-layer cause an eligible
	// subject the run granted nothing is named by
	// (REQ-check-witness-selection).
	pkgDisp map[string]stipulatorv1.HealthDisposition
}

// packageCause answers the recorded disposition of a package's process
// under an invocation as its cause, with the package-scoped diagnostic's
// reason when the merge holds one.
func (m *execMerge) packageCause(invocation, pkg string) (string, bool) {
	disposition, ok := m.pkgDisp[invPkgKey(invocation, pkg)]
	if !ok {
		return "", false
	}
	return dispositionCause(invocation, pkg, disposition, packageReason(m.diags, invocation, pkg, disposition)), true
}

// absorb folds a later pass's merge into this one — its rows and diagnostics
// appended, its producers' dispositions and observations joined (a retry's
// producers are its own: a producer key carries the process id and ordinal,
// so a later pass's processes never collide with an earlier pass's) — and its
// package dispositions overlaid per package, the worse of the two standing: a
// retry timeout is the cause of what it denied, and a healthy retry over the
// drifted subjects alone never erases a red first pass that denied the rest —
// the run's own worst-outcome discipline, applied to causes. One merge is
// then consumed, so the overlay cannot be skipped on the way to the report.
func (m *execMerge) absorb(later *execMerge) {
	m.rows = append(m.rows, later.rows...)
	m.diags = append(m.diags, later.diags...)
	for key, disposition := range later.disp {
		m.disp[key] = disposition
	}
	for key, observation := range later.obs {
		m.obs[key] = observation
	}
	if len(later.denied) != 0 && m.denied == nil {
		m.denied = map[producerSubject]uncacheable{}
	}
	for key, reason := range later.denied {
		m.denied[key] = reason
	}
	for key, disposition := range later.pkgDisp {
		if first, ok := m.pkgDisp[key]; ok {
			disposition = worseDisposition(first, disposition)
		}
		m.pkgDisp[key] = disposition
	}
}

// invPkgKey is the one spelling of the per-invocation package key
// every package-scoped index shares.
func invPkgKey(invocation, pkg string) string {
	return invocation + "\x00" + pkg
}

func newExecMerge() *execMerge {
	return &execMerge{
		disp:    map[producerKey]stipulatorv1.HealthDisposition{},
		obs:     map[producerKey]*ProcessObservation{},
		denied:  map[producerSubject]uncacheable{},
		pkgDisp: map[string]stipulatorv1.HealthDisposition{},
	}
}

// addUnit absorbs one package's completed unit — its process and solo
// re-runs — the moment it completes, so the package's evidence is in the
// merge when its records publish.
func (m *execMerge) addUnit(invocation string, unit packageUnit) {
	res := &SelectionResult{}
	res.absorb(unit)
	m.add(invocation, res)
}

func (m *execMerge) add(invocation string, res *SelectionResult) {
	m.rows = append(m.rows, res.Tests...)
	m.diags = append(m.diags, res.Diagnostics...)
	for _, p := range res.Processes {
		if p.Producer != nil {
			m.disp[keyOfProducer(p.Producer)] = p.Disposition
		}
		// A package's process carries its disposition whether or not it
		// spawned — an envelope denied before the spawn disposes it too.
		if p.Package != "" && p.Test == "" {
			m.pkgDisp[invPkgKey(invocation, p.Package)] = p.Disposition
		}
	}
	for _, o := range res.Observations {
		m.obs[keyOfProducer(o.Wire.GetProducer())] = o
	}
}

// executeSelections runs the invocations serially; onPackage, when
// non-nil, fires the moment each package's unit completes, its unit
// already in the merge — the hook that lets the package publish and
// install while its siblings still execute, so a run dying
// mid-execution keeps every record already produced
// (REQ-evidence-witness-cache-format-install's install-on-completion rule).
func executeSelections(ctx context.Context, invocations []*NormalizedInvocation, staleSel map[string]TestSelection, m *execMerge, onPackage func(invocation string, unit packageUnit) error, proofs processProofs, ordinal func() int32) error {
	for _, n := range invocations {
		sel := staleSel[n.Name]
		if len(sel) == 0 {
			continue
		}
		var hook func(packageUnit) error
		if onPackage != nil {
			// With a package hook the merge absorbs each unit as it
			// completes, so the invocation's result is not added again.
			hook = func(unit packageUnit) error {
				m.addUnit(n.Name, unit)
				return onPackage(n.Name, unit)
			}
		}
		res, err := executeSelectionObserved(ctx, n, sel, hook, proofs[n.Name], ordinal)
		if err != nil {
			return err
		}
		if onPackage == nil {
			m.add(n.Name, res)
		}
	}
	return nil
}

// finishGroup finishes the group's packages in the verification pass,
// after every execution of the run has completed: every package not yet
// published publishes (the arm for packages nothing executed — their
// subjects' reasons), and every package with served records revalidates
// them (REQ-check-witness-selection's post-run revalidation), its drifted
// subjects returned for the run's one retry.
func finishGroup(ctx context.Context, wg *witnessGroup, m *execMerge) ([]gofresh.Subject, []witnesscache.Record, map[gofresh.Subject]uncacheable, error) {
	var drifted []gofresh.Subject
	var records []witnesscache.Record
	reasons := map[gofresh.Subject]uncacheable{}
	for _, pkg := range wg.packages() {
		leg := wg.legs[pkg]
		if !leg.published {
			r, why, err := publishPackage(ctx, wg, pkg, m)
			if err != nil {
				return nil, nil, nil, err
			}
			records = append(records, r...)
			maps.Copy(reasons, why)
		}
		d, r, why := revalidateServed(ctx, wg, pkg)
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		drifted = append(drifted, d...)
		records = append(records, r...)
		maps.Copy(reasons, why)
	}
	return drifted, records, reasons, nil
}

// beforePackagePublishForTest, when set, runs before a package's
// executed records publish, on either form — the seam a test uses to
// move an input between the package's process and its publish.
var beforePackagePublishForTest func(pkg string)

// afterServedCheckForTest runs after executions and before closing the historical
// checking transaction, allowing tests to move an input in its provisional span.
var afterServedCheckForTest func(pkg string)

// publishPackage publishes one package's executed subjects' new records
// the moment the package completes — the unit of persistence
// (REQ-policy-cancellation-unit) — from independently native-closed attempts.
// Its served records are not
// judged here: the clause revalidates them after the run's executions
// complete (revalidateServed), so a sibling package's later write still
// disproves a serve the run would otherwise report. The error return is
// reserved for caller cancellation. A package serving nothing releases
// its sibling here.
func publishPackage(ctx context.Context, wg *witnessGroup, pkg string, m *execMerge) ([]witnesscache.Record, map[gofresh.Subject]uncacheable, error) {
	wg.legs[pkg].published = true
	records, reasons, _, err := publishExecuted(ctx, wg, pkg, m)
	if err != nil {
		return nil, nil, err
	}
	if len(wg.servedIn(pkg)) == 0 {
		wg.release(pkg)
	}
	return records, reasons, nil
}

// release drops a package's leg once nothing more reads it and, once
// every leg of the group has released, the group's view: the memory a
// finished package's analysis held returns while its siblings still
// execute.
func (wg *witnessGroup) release(pkg string) {
	wg.legs[pkg].release()
	if legsReleased(wg.legs) {
		wg.view = nil
	}
}

// revalidateServed revalidates one package's served records against the
// tree the run finished on — after every execution of the run, never at
// the package's own completion, so a sibling package's later write
// still disproves a serve (REQ-check-witness-selection). A served record
// that no longer checks valid drifted mid-run: its served outcome is
// discarded and the subject joins the drifted set for the run's single
// retry; a check that faults, or a closing validation that refuses,
// discards every serve of the package the same way; a carve-out-served
// record that survived installs refreshed. The package's sibling is
// released after. A cancellation surfaces through ctx.
func revalidateServed(ctx context.Context, wg *witnessGroup, pkg string) ([]gofresh.Subject, []witnesscache.Record, map[gofresh.Subject]uncacheable) {
	leg := wg.legs[pkg]
	defer wg.release(pkg)
	served := wg.servedIn(pkg)
	reasons := map[gofresh.Subject]uncacheable{}
	if len(served) == 0 {
		return nil, nil, reasons
	}
	if afterServedCheckForTest != nil {
		afterServedCheckForTest(pkg)
	}
	verdicts, err := leg.checking.Close(ctx)
	if err == nil {
		err = leg.validateInputs(ctx)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, reasons
		}
		// The discarded serves re-execute holding prior evidence, so each
		// names why serving refused it — the same attribution the
		// validation-refusal arm carries.
		for _, s := range served {
			wg.executedWhy[s] = "post-run served-record revalidation faulted: " + err.Error()
		}
		return served, nil, reasons
	}
	var drifted []gofresh.Subject
	var records []witnesscache.Record
	for _, s := range served {
		chosen := leg.chosen[s]
		checked := verdicts[chosen.index]
		if checked.Verdict.Status != gofresh.Valid {
			drifted = append(drifted, s)
			// The served record held prior evidence and now re-executes:
			// the drift verdict's reason - movers named - is its
			// executed-reason attribution, never a cold read.
			wg.executedWhy[s] = "mid-run drift: " + checked.Verdict.Reason
			continue
		}
		// A carve-out-served record that survived post-run revalidation
		// installs its effective applicability endpoint and ledger — so
		// the next run reads it plainly valid instead of re-proving the
		// same inert delta.
		if checked.Extension != nil {
			rec := chosen.record
			rec.Fingerprint = checked.Extension.Fingerprint
			rec.CompartmentLedger = witnesscache.LedgerFromGofresh(checked.Extension.Ledger)
			chosen.record = rec
			leg.chosen[s] = chosen
			records = append(records, rec)
		}
	}
	return drifted, records, reasons
}

// publishExecuted selects independently eligible native-finalized attempts.
// Refused native judgments never become records; actual outcomes remain in the
// execution account independently of publication.
// The error return is reserved for caller cancellation. The second
// return names, per unpublished subject, the leg that refused
// (REQ-evidence-witness-freshness-diagnosable's diagnosable-set requirement).
func publishExecuted(ctx context.Context, wg *witnessGroup, pkg string, m *execMerge) ([]witnesscache.Record, map[gofresh.Subject]uncacheable, bool, error) {
	order := make([]gofresh.Subject, 0, len(wg.stale[pkg]))
	for _, name := range wg.stale[pkg] {
		order = append(order, gofresh.Subject{Package: pkg, Symbol: name})
	}
	eligible := map[gofresh.Subject]*pubSubject{}
	reasons := map[gofresh.Subject]uncacheable{}
	for _, s := range order {
		why, refused := wg.g.neverServes[s]
		ps, reason := judgeSubject(s, why, refused, true, producersOf(s, wg.g, m))
		if ps == nil {
			reasons[s] = reason
			continue
		}
		eligible[s] = ps
	}
	// No served outcome is at stake here: the served half is revalidated
	// after the run's executions complete (revalidateServed), so this
	// close gates the executed records alone.
	leg := wg.legs[pkg]
	records, _, fatal := publishEligible(ctx, wg.g.id, leg, order, eligible, declarationsFor(wg.g), reasons)
	if fatal != nil {
		return nil, nil, false, fatal
	}
	return records, reasons, false, nil
}

// retryDrifted re-executes each drifted served subject exactly once,
// within the same run, under its covering invocation's envelope: the
// served outcome was already discarded, so the retry's execution is the
// subject's outcome for this run. New native snapshots precede the wave;
// packages deposit independently at completion. Demonstrated retry drift denies
// passing grants separately from ordinary uncacheability and raw process facts.
func retryDrifted(ctx context.Context, pc *Capture, groups []*witnessGroup, driftedByGroup map[*witnessGroup][]gofresh.Subject, m *execMerge, resources *retryResources, install func([]witnesscache.Record) ([]witnesscache.Record, error), ordinal func() int32) ([]*retryCandidate, map[gofresh.Subject]uncacheable, error) {
	dir := pc.dir
	normalized := pc.byName()
	// Fresh pre-retry capture per group: the old view described a tree the
	// drift already left behind.
	type retryState struct {
		wg       *witnessGroup
		view     *witnessSnapshot
		subjects []gofresh.Subject
		legs     map[string]*packageLeg
	}
	var states []retryState
	reasons := map[gofresh.Subject]uncacheable{}
	retrySel := map[string]TestSelection{}
	for _, wg := range groups {
		subjects := driftedByGroup[wg]
		if len(subjects) == 0 {
			continue
		}
		sortSubjects(subjects)
		for _, s := range subjects {
			n := normalized[wg.g.selectingInvocation(s.Package)]
			sel := retrySel[n.Name]
			if sel == nil {
				sel = TestSelection{}
				retrySel[n.Name] = sel
			}
			sel[s.Package] = append(sel[s.Package], s.Symbol)
		}
		st := retryState{wg: wg, subjects: subjects, legs: map[string]*packageLeg{}}
		// A failed view or capture leaves the retry unpublishable — it
		// still executes, its outcome still witnesses, its record is
		// simply dropped and counted uncacheable.
		view, inputs, err := groupSnapshot(ctx, dir, wg.g)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if abort, _ := classifyFault(err); abort {
				return nil, nil, err
			}
			for _, s := range subjects {
				reasons[s] = reasonNoCapture.with("retry engine: " + err.Error())
			}
			states = append(states, st)
			continue
		}
		st.view = view
		// Retry packages receive new runtime bindings on the newly observed
		// build generation. Each actual process independently registers the
		// effective runnable roster; a failed preparation cannot publish.
		byPkg := map[string][]gofresh.Subject{}
		for _, s := range subjects {
			byPkg[s.Package] = append(byPkg[s.Package], s)
		}
		for pkg := range byPkg {
			if st.view == nil {
				break
			}
			leg, err := nativePackageLeg(ctx, st.view, inputs, normalized[wg.g.selectingInvocation(pkg)], pkg)
			if err != nil {
				st.view = nil
				break
			}
			st.legs[pkg] = leg
			resources.release = append(resources.release, leg.dispose)
		}
		states = append(states, st)
	}
	proofs := processProofs{}
	for _, st := range states {
		proofs.add(st.wg.g, st.legs)
	}
	var candidates []*retryCandidate
	onPackage := func(invocation string, unit packageUnit) error {
		resources.retain(unit)
		for _, st := range states {
			if st.wg.g.selectingInvocation(unit.pkg) != invocation {
				continue
			}
			var names []string
			for _, subject := range st.subjects {
				if subject.Package == unit.pkg {
					names = append(names, subject.Symbol)
				}
			}
			if len(names) == 0 {
				continue
			}
			current := collectRetryCandidates(st.wg, unit, m, reasons)
			candidates = append(candidates, current...)
			leg := st.legs[unit.pkg]
			if st.view == nil || leg == nil {
				continue
			}
			// Later waves build new snapshots. Keep only runtime storage through
			// final consumption, rather than retaining completed source graphs.
			defer leg.release()
			rwg := &witnessGroup{g: st.wg.g, view: st.view, legs: st.legs, stale: map[string][]string{unit.pkg: names}, executedWhy: map[gofresh.Subject]string{}}
			records, retryReasons, _, err := publishExecuted(ctx, rwg, unit.pkg, m)
			maps.Copy(reasons, retryReasons)
			if err != nil {
				return err
			}
			leg.preparation.Lock()
			inputErr := leg.inputErr
			leg.preparation.Unlock()
			if demonstratedDrift(inputErr) {
				for _, item := range current {
					reason := reasonSourceFailed.with(inputErr.Error())
					m.denied[item.key] = reason
					reasons[item.key.subject] = reason
					if item.native != nil {
						item.native.reusable = false
					}
				}
			}
			landed, err := install(records)
			noteRetryPublication(ctx, invocation, unit.pkg, landed)
			for _, record := range landed {
				subject := gofresh.Subject{Package: record.Package, Symbol: record.Test}
				why, refused := st.wg.g.neverServes[subject]
				selected, _ := judgeSubject(subject, why, refused, true, producersOf(subject, st.wg.g, m))
				if selected == nil {
					continue
				}
				key := producerSubject{producer: keyOfProducer(selected.obs.Wire.GetProducer()), subject: subject}
				for _, item := range current {
					if item.key == key && item.native != nil {
						copy := record
						item.native.record = &copy
						break
					}
				}
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	if err := executeSelections(ctx, pc.normalized, retrySel, m, onPackage, proofs, ordinal); err != nil {
		return candidates, reasons, err
	}
	return candidates, reasons, nil
}

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

// consumeMerge folds one merged selective execution into the run's
// witness-evidence view: failed and skipped results are recorded
// regardless — red is a fact whatever produced it — while a pass grants
// an outcome only from a process whose own disposition is healthy, so a
// completed pass inside a red process reads unwitnessed unless its
// isolation re-run granted it solo (REQ-evidence-witness-freshness-isolation's
// isolation sentence). When one test name carries several results the
// worst outcome wins, so a single red occurrence is never papered over
// by a green sibling.
func consumeMerge(tr *verify.TestRun, m *execMerge, ranTop map[string]bool, plainInv, raceGranted, plainGranted map[string]bool) {
	tr.Diagnostics = append(tr.Diagnostics, m.diags...)
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
			_, denied := m.denied[producerSubject{producer: keyOfProducer(row.GetProducer()), subject: gofresh.Subject{Package: pkg, Symbol: topLevel(test)}}]
			if !denied && m.disp[keyOfProducer(row.GetProducer())] == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
				outcome = verify.TestPassed
				if plainInv[row.GetProducer().GetInvocation()] {
					plainGranted[key] = true
				} else {
					raceGranted[key] = true
				}
			}
		}
		if outcome != verify.TestNotRun && verify.OutcomeRank(outcome) > verify.OutcomeRank(tr.Outcomes[key]) {
			tr.Outcomes[key] = outcome
		}
		for _, req := range row.GetRegistrations() {
			tr.Registrations = append(tr.Registrations, verify.Registration{
				Package: pkg, Test: test, Requirement: req,
			})
		}
		if key, ok := executedTopKey(pkg, test); ok {
			ranTop[key] = true
		}
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
