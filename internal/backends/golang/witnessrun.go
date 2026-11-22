package golang

import (
	"context"
	"fmt"
	"maps"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	gofresh "github.com/greatliontech/gofresh"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/trace"
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

func runWitnesses(ctx context.Context, pc *Capture, scope map[gofresh.Subject]bool, seeding verify.WitnessSeeding) (*verify.TestRun, error) {
	if err := pc.ConfigureExecution(false, scope); err != nil {
		return nil, err
	}
	rep := progress.FromContext(ctx)
	if rep == nil {
		rep = progress.New(nil)
		ctx = progress.NewContext(ctx, rep)
	}
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
			if pc.execution.permits(s) {
				ambiguousSubjects[s] = true
			}
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
			if wg.g != nil {
				wg.g.sessionState = nil
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
		prepStart := time.Now()
		groups, degraded, prepErr = prepareWitnessGroups(ctx, dir, d, cachedByKey)
		trace.Emit(ctx, trace.DomainWitness, "prepare_witness_groups", dir,
			trace.Int("groups", len(groups)),
			trace.String("degraded", degraded),
			trace.Int64("duration_ms", time.Since(prepStart).Milliseconds()),
			trace.Err(prepErr),
		)
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
				kept := names[:0]
				for _, name := range names {
					s := gofresh.Subject{Package: pkg, Symbol: name}
					addStale(wg.g, s)
					if pc.execution.permits(s) {
						kept = append(kept, name)
					}
				}
				if len(kept) == 0 {
					delete(wg.stale, pkg)
					if len(wg.servedIn(pkg)) == 0 {
						wg.release(pkg)
					}
				} else {
					wg.stale[pkg] = kept
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
						reasons[pkg+"."+name] = formatExecutedReason(why)
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
	// caller's scope left out executes nothing and releases its leg
	// before execution; an in-scope stale package an envelope cutoff
	// prevented from completing publishes its reasons in the verification pass.
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
	type servedGrant struct {
		record witnesscache.Record
		plain  bool
	}
	var servedRecords []servedGrant
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
				n := normalized[wg.g.selectingInvocation(s.Package)]
				servedRecords = append(servedRecords, servedGrant{
					record: wg.selectedRecord(s),
					plain:  n != nil && !n.Race && n.PlainWitness,
				})
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
			if tr.ExecutedReasons == nil {
				tr.ExecutedReasons = map[string]string{}
			}
			tr.ExecutedReasons[s.Package+"."+s.Symbol] = formatExecutedReason(why)
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
					why := executedFromUncacheable(reasonStoreRefused.with(fmt.Sprintf("load witness record (%s): %s", refusal.GetCause(), refusal.GetDetail())))
					if policyReason, refused := g.neverServes[s]; refused {
						why = executedFromUncacheable(policyReason)
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
	for _, sg := range servedRecords {
		rec := sg.record
		for key, out := range rec.Outcomes {
			o := outcomeFromString(out)
			if verify.OutcomeRank(o) > verify.OutcomeRank(tr.Outcomes[key]) {
				tr.Outcomes[key] = o
			}
			if o == verify.TestPassed {
				if sg.plain {
					plainGranted[key] = true
				} else {
					raceGranted[key] = true
				}
			}
		}
		tr.Registrations = append(tr.Registrations, rec.Regs...)
	}
	// Fresh counts served subjects, not served records: a subject two
	// groups serve is one saved test in the same unit Ran counts
	// executed tests.
	servedKey := map[string]bool{}
	for _, sg := range servedRecords {
		rec := sg.record
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
	fallback := reasonNotPublished.with("")
	if degraded != "" {
		fallback = reasonDegraded.with(degraded)
	}
	reconcileUncached(tr, ranTop, published, servedKey, uncacheableWhy, ambiguousSubjects, fallback)

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
	servedByInv := map[string]map[string]bool{}
	for _, wg := range groups {
		for _, s := range wg.served {
			inv := wg.g.selectingInvocation(s.Package)
			if inv == "" {
				continue
			}
			if servedByInv[inv] == nil {
				servedByInv[inv] = map[string]bool{}
			}
			servedByInv[inv][s.Package+"."+s.Symbol] = true
		}
	}
	allRows := append([]*stipulatorv1.TestResult(nil), m.rows...)
	if ineligibleMerge != nil {
		allRows = append(allRows, ineligibleMerge.rows...)
	}
	tr.InvocationWitnesses = buildInvocationWitnessAccounts(
		d,
		allRows,
		servedByInv,
		publishedByInvocation(d.groups, published),
		tr.NoOutcome,
		scopeSkipped,
	)
	tr.Timing = rep.TimingDiagnostics()
	// Publication installed at production: each record landed as its own
	// variant file, atomically, the moment its group finished — at its
	// last covering invocation's completion for executing groups, in the
	// verification pass for all-served groups, after the retry for
	// drifted subjects. Untouched records — served, outside-policy,
	// departed — need no rewrite, and on the degraded path nothing
	// executed under groups, so nothing installed.
	return tr, nil
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
