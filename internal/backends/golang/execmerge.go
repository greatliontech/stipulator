package golang

import (
	"context"
	"maps"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/trace"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

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
		if p.Package != "" {
			key := invPkgKey(invocation, p.Package)
			if p.Test == "" {
				if prev, ok := m.pkgDisp[key]; ok {
					m.pkgDisp[key] = worseDisposition(prev, p.Disposition)
				} else {
					m.pkgDisp[key] = p.Disposition
				}
			} else if p.Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
				m.pkgDisp[key] = worseDisposition(m.pkgDisp[key], p.Disposition)
			}
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
	pubStart := time.Now()
	wg.legs[pkg].published = true
	records, reasons, _, err := publishExecuted(ctx, wg, pkg, m)
	trace.Emit(ctx, trace.DomainWitness, "publish_package", pkg,
		trace.Int("records", len(records)),
		trace.Int("uncacheable", len(reasons)),
		trace.Int64("duration_ms", time.Since(pubStart).Milliseconds()),
		trace.Err(err),
	)
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
	revStart := time.Now()
	leg := wg.legs[pkg]
	defer wg.release(pkg)
	served := wg.servedIn(pkg)
	reasons := map[gofresh.Subject]uncacheable{}
	if len(served) == 0 {
		return nil, nil, reasons
	}
	var driftedCount, recordCount int
	defer func() {
		trace.Emit(ctx, trace.DomainWitness, "revalidate_served", pkg,
			trace.Int("served", len(served)),
			trace.Int("drifted", driftedCount),
			trace.Int("refreshed", recordCount),
			trace.Int64("duration_ms", time.Since(revStart).Milliseconds()),
		)
	}()
	if afterServedCheckForTest != nil {
		afterServedCheckForTest(pkg)
	}
	var needBatch, needSolo bool
	for _, s := range served {
		if leg.chosen[s].solo {
			needSolo = true
		} else {
			needBatch = true
		}
	}
	var batchVerdicts, soloVerdicts []gofresh.CheckedCandidate
	var err error
	if needBatch {
		batchVerdicts, err = leg.checking.Close(ctx)
	}
	if err == nil && needSolo {
		soloVerdicts, err = leg.soloChecking.Close(ctx)
	}
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
			wg.executedWhy[s] = executedRevalidationFault.with(err.Error())
		}
		driftedCount = len(served)
		return served, nil, reasons
	}
	var drifted []gofresh.Subject
	var records []witnesscache.Record
	defer func() {
		driftedCount = len(drifted)
		recordCount = len(records)
	}()
	for _, s := range served {
		chosen := leg.chosen[s]
		verdicts := batchVerdicts
		if chosen.solo {
			verdicts = soloVerdicts
		}
		checked := verdicts[chosen.index]
		if checked.Verdict.Status != gofresh.Valid {
			drifted = append(drifted, s)
			// The served record held prior evidence and now re-executes:
			// the drift verdict's reason - movers named - is its
			// executed-reason attribution, never a cold read.
			wg.executedWhy[s] = executedMidRunDrift.with(checked.Verdict.Reason)
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
		view, inputs, err := groupSnapshot(ctx, dir, wg.g.subsetForSubjects(subjects))
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
			rwg := &witnessGroup{g: st.wg.g, view: st.view, legs: st.legs, stale: map[string][]string{unit.pkg: names}, executedWhy: map[gofresh.Subject]executedReason{}}
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
