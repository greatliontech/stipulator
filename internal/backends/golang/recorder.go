package golang

import (
	"context"
	"fmt"
	"maps"
	"runtime/debug"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// NewWitnessRecorder prepares freshness publication for one execution of
// the accepted policy: it must be called before the policy executes, so
// the shared snapshots pin the tree the execution compiles, and
// the caller must close the recorder after executions have joined and report
// every package's completion under its covering
// invocation through the completion hook — a package whose completion
// is never reported publishes nothing, with no fallback. Only race-enabled
// or explicitly plain-admitted Go invocations are captured. An unadmitted
// non-race invocation grants no witness evidence, so nothing it produces may
// enter the cache a freshness-serving run would grant evidence from. The seeding
// classification is the recorder's one question to its backend, and the
// backend is released right after it (verify.Quiescer) — before the
// engines' loads below, so the backend's child is never resident beside
// them or the execution. A fault while preparing disables publication
// and is reported through the derived run's degraded reason, never as
// an error — publication is optimization, not correctness — with one
// exception: a toolchain-provenance refusal returns as the error, a
// run-level abort (REQ-evidence-toolchain-provenance; classifyFault),
// because the refused frontend also discovered and selected the suite
// the degraded run would execute.
func NewWitnessRecorder(ctx context.Context, pc *Capture, seeding verify.WitnessSeeding) (*WitnessRecorder, error) {
	if err := pc.ConfigureExecution(true, nil); err != nil {
		return nil, err
	}
	dir := pc.dir
	r := &WitnessRecorder{dir: dir, reasons: map[gofresh.Subject]uncacheable{}}
	degrade := func(err error) (*WitnessRecorder, error) {
		abort, reason := classifyFault(err)
		if abort {
			return nil, err
		}
		r.degraded = reason
		r.groups = nil
		return r, nil
	}
	d, err := pc.discover(ctx)
	if err != nil {
		return degrade(err)
	}
	defer func() {
		if r.degraded != "" {
			for _, g := range d.groups {
				for _, leg := range g.legs {
					leg.dispose()
				}
				g.view = nil
			}
		}
	}()
	// A seeding-classification fault degrades the run exactly as an
	// engine fault does: nothing publishes, so nothing can later serve
	// a witness the fault left unclassified (fail closed).
	seedingErr := classifySeeded(d, seeding)
	// The classification was this form's last question to its backend:
	// released before the engines' loads and the first spawn
	// (REQ-evidence-resolution-freshness-quiesce).
	quiesceSeeding(seeding)
	if seedingErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.degraded = seedingErr.Error()
		return r, nil
	}
	// A package two invocations of one group select has no producing
	// leg: the refusal is a discovery fact, recorded once here for
	// every group — the group a double selection empties never
	// publishes, so its subjects' reason has no later home. The first
	// selecting invocation's names are every subject: the group key
	// holds every dimension that moves a package's listed tests, so two
	// invocations of one group list one package identically.
	for _, g := range d.groups {
		for pkg, p := range g.packages {
			if !p.ambiguous {
				continue
			}
			for _, name := range p.names {
				r.reasons[gofresh.Subject{Package: pkg, Symbol: name}] = reasonNoProducingLeg
			}
		}
	}
	for g, subjects := range d.populatedGroups() {
		view, inputs, err := groupSnapshot(ctx, dir, g)
		if err != nil {
			return degrade(err)
		}
		g.view = view
		byPkg := map[string][]gofresh.Subject{}
		for _, s := range subjects {
			byPkg[s.Package] = append(byPkg[s.Package], s)
		}
		g.legs = map[string]*packageLeg{}
		for pkg := range byPkg {
			leg, err := nativePackageLeg(ctx, g.view, inputs, pc.byName()[g.selectingInvocation(pkg)], pkg)
			if err != nil {
				return degrade(err)
			}
			g.legs[pkg] = leg
		}
		r.groups = append(r.groups, g)
	}
	// Release transient package-loading memory before the caller spawns
	// race-instrumented builds; the views stay alive for post-execution
	// producer validation.
	debug.FreeOSMemory()
	return r, nil
}

// producerKey identifies one producing process for observation lookup.
type producerKey struct {
	invocation string
	pid        int64
	ordinal    int32
}

func keyOfProducer(p *stipulatorv1.ProducerIdentity) producerKey {
	return producerKey{invocation: p.GetInvocation(), pid: p.GetProcessId(), ordinal: p.GetProcessOrdinal()}
}

// Derive turns one execution report into the run's witness-evidence view
// and publishes per-test freshness records from it. Evidence and health
// come from DeriveTestRun alone; publication then covers exactly the
// tests whose producing package disposed healthy inside a race-enabled
// invocation and whose producing process owns a completed observation —
// that process's own observation and never a sibling's, because an
// observation proves only what its own process read. Records survive to
// the cache only after the analysis views revalidate (source producer
// validation) and each fingerprint's post-run check returns valid
// (runtime producer validation); a record whose inputs moved during
// execution, or whose observation is unverifiable, is dropped and counted
// uncacheable rather than published. The error return is reserved for
// caller cancellation.
func (r *WitnessRecorder) Derive(ctx context.Context, report *stipulatorv1.ExecutionReport) (*verify.TestRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tr := DeriveTestRun(report)
	published, uncacheableWhy, degraded := r.publish()
	executedTop := executedTopKeys(report)
	switch {
	case degraded != "":
		// The fault ended publication; what earlier groups installed
		// stays — each validated by its own closing check — and only
		// the rest reads uncacheable, so the run tells one story with
		// the store and its ending.
		tr.Degraded = degraded
		installed := map[string]bool{}
		for _, rec := range published {
			installed[rec.Package+"."+rec.Test] = true
		}
		// A subject the ladder refused before the fault keeps the
		// ladder's own reason; the degrade explains only the rest.
		tr.Uncached = 0
		tr.UncacheableReasons = map[string]string{}
		for s, why := range uncacheableWhy {
			tr.UncacheableReasons[s.Package+"."+s.Symbol] = why.String()
		}
		for key := range executedTop {
			if installed[key] {
				delete(tr.UncacheableReasons, key)
				continue
			}
			tr.Uncached++
			if _, ok := tr.UncacheableReasons[key]; !ok {
				tr.UncacheableReasons[key] = reasonDegraded.with(degraded).String()
			}
		}
	case len(r.groups) == 0:
		// Nothing was capturable (no witness-eligible invocation, no
		// expected tests, or every eligible package doubly selected):
		// every executed test is uncacheable — under its recorded
		// refusal where one stands — and the existing cache is left
		// alone.
		tr.Uncached = tr.Ran
		tr.UncacheableReasons = map[string]string{}
		for s, why := range uncacheableWhy {
			tr.UncacheableReasons[s.Package+"."+s.Symbol] = why.String()
		}
		for key := range executedTop {
			if _, ok := tr.UncacheableReasons[key]; !ok {
				tr.UncacheableReasons[key] = reasonNoCapture.with("no witness-eligible invocation covers the package").String()
			}
		}
	default:
		// Published is a record count (one per group holding the
		// subject); Uncached is a subject count in Ran's unit, so
		// distinct published subjects are the subtrahend — two group
		// records for one shared package must not mask another
		// subject's drop.
		publishedSubjects := map[string]bool{}
		for _, rec := range published {
			publishedSubjects[rec.Package+"."+rec.Test] = true
		}
		if tr.Ran > len(publishedSubjects) {
			tr.Uncached = tr.Ran - len(publishedSubjects)
		}
		// Per-test attribution mirrors the selective runner's: the
		// ladder's own refusal reasons plus a structural fallback
		// (REQ-evidence-witness-freshness-diagnosable's attributable
		// reason).
		if tr.Uncached > 0 || len(uncacheableWhy) > 0 {
			tr.UncacheableReasons = map[string]string{}
			for s, why := range uncacheableWhy {
				tr.UncacheableReasons[s.Package+"."+s.Symbol] = why.String()
			}
			publishedKey := map[string]bool{}
			for _, rec := range published {
				publishedKey[rec.Package+"."+rec.Test] = true
			}
			for key := range executedTop {
				if publishedKey[key] {
					delete(tr.UncacheableReasons, key)
					continue
				}
				if _, ok := tr.UncacheableReasons[key]; !ok {
					tr.UncacheableReasons[key] = reasonNotPublished.with("").String()
				}
			}
		}
		// Publication installed at production: each package's records
		// landed as their own variant files the moment the package
		// completed under its covering invocation. Records this
		// run never touched — a shadowed sibling's, a package this policy
		// never selected's — need no rewrite: the store is per-record, so
		// retention is the default and nothing shrinks. A departed test's
		// variants linger as dead weight — its identity never installs
		// again, so no bound fires; store growth is cost, never
		// correctness.
	}
	return tr, nil
}

// packageCompleted is the completion hook of the health-judged form:
// the moment a package's process has completed under its covering
// invocation, every group holding the package under that invocation
// publishes the package's records — from the process's own rows,
// disposition and observation — and installs them at once, named on
// the progress stream by invocation and package: the unit of
// persistence is the package under its covering invocation on this
// form as on the selective one (REQ-policy-cancellation-unit). A
// publication fault degrades the run's further publication, as a
// group's did; the error return is reserved for caller cancellation.
func (r *WitnessRecorder) packageCompleted(ctx context.Context, invocation string, unit packageUnit) error {
	if r.degraded != "" {
		return nil
	}
	// The package's one producing process under its covering invocation
	// is every subject's single candidate, rows or none: the executor
	// launches exactly one process per selected package per invocation,
	// and a package with no row still carries its disposition.
	candidate := producerCandidate{
		healthy: unit.run.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
		rows:    unit.run.tests,
		obs:     unit.run.obs,
	}
	var soloCandidates []producerCandidate
	for _, s := range unit.solos {
		soloCandidates = append(soloCandidates, producerCandidate{
			healthy: s.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
			rows:    s.tests,
			obs:     s.obs,
		})
	}
	for _, g := range r.groups {
		// One package can sit in several groups — two invocations under
		// different build coordinates both selecting it — each group
		// running it under its own invocation; the completing invocation
		// publishes the package in its own group alone. A package two
		// invocations of one group select was refused at discovery.
		p, ok := g.packages[unit.pkg]
		if !ok || p.ambiguous || p.inv != invocation {
			continue
		}
		// Every non-ambiguous package of a populated group has a leg —
		// its entry carries at least one expected test, and the legs
		// were built over the group's subjects — and completes once
		// under its invocation, so the leg is not yet published.
		records, reasons, degraded, err := r.publishPackage(ctx, g, unit.pkg, g.legs[unit.pkg], candidate, soloCandidates)
		if err != nil {
			return err
		}
		if degraded != "" {
			r.degrade(degraded)
			return nil
		}
		maps.Copy(r.reasons, reasons)
		landed, installErr := installRecords(ctx, r.dir, records, r.reasons)
		r.records = append(r.records, landed...)
		// What landed is named before anything else: a cancellation
		// between two packages' installs must not leave records on disk
		// the ending never mentions.
		if len(landed) > 0 {
			progress.FromContext(ctx).Persisted(invocation+" "+unit.pkg, len(landed))
		}
		if installErr != nil {
			return installErr
		}
	}
	return nil
}

// publish is the run's publication account: every record the
// completion hook installed and every refusal reason, with the degraded
// reason when a later package's fault ended publication. Nothing is
// left to publish here: the recorder's groups are the populated groups
// of the discovery the executor walks, each holding a package exactly
// one of its invocations selects, and the executor fires every
// package's completion under its invocation or returns its fault
// before derivation — so every package is published by the time the
// last invocation completes.
func (r *WitnessRecorder) publish() ([]witnesscache.Record, map[gofresh.Subject]uncacheable, string) {
	return append([]witnesscache.Record(nil), r.records...), maps.Clone(r.reasons), r.degraded
}

// publishPackage selects native-finalized records from this package's actual
// process and applies the consumer's outcome and opaque-input gates.
// The leg is released after — nothing serves on this form, so
// nothing reads it again — and the group's view with it once every
// leg has published. A source-view, configuration or infrastructure fault ends
// further publication with its named cause; earlier packages keep what their
// own legs independently validated. Demonstrated runtime-input movement refuses
// this process's records locally; its account and execution facts remain intact.
// The error return is reserved for caller cancellation.
func (r *WitnessRecorder) publishPackage(ctx context.Context, g *captureGroup, pkg string, leg *packageLeg, candidate producerCandidate, soloCandidates []producerCandidate) ([]witnesscache.Record, map[gofresh.Subject]uncacheable, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, "", err
	}
	defer g.release(pkg)
	if candidate.obs != nil && candidate.obs.closeErr != nil && !runtimeInputMovement(candidate.obs.closeErr) {
		if ctx.Err() != nil {
			return nil, nil, "", ctx.Err()
		}
		return nil, nil, "runtime producer validation failed: " + candidate.obs.closeErr.Error(), nil
	}
	for _, sc := range soloCandidates {
		if sc.obs != nil && sc.obs.closeErr != nil && !runtimeInputMovement(sc.obs.closeErr) {
			if ctx.Err() != nil {
				return nil, nil, "", ctx.Err()
			}
			return nil, nil, "runtime producer validation failed: " + sc.obs.closeErr.Error(), nil
		}
	}
	reasons := map[gofresh.Subject]uncacheable{}
	eligible := map[gofresh.Subject]*pubSubject{}
	var order []gofresh.Subject
	candidates := []producerCandidate{candidate}
	for _, name := range g.packages[pkg].names {
		subject := gofresh.Subject{Package: pkg, Symbol: name}
		why, refused := g.neverServes[subject]
		subCandidates := candidates
		if leg != nil && leg.view != nil && leg.view.isSolo(subject) && len(soloCandidates) > 0 {
			subCandidates = soloCandidates
		}
		ps, reason := judgeSubject(subject, why, refused, true, subCandidates)
		if ps == nil {
			reasons[subject] = reason
			continue
		}
		eligible[subject] = ps
		order = append(order, subject)
	}
	sortSubjects(order)
	// Native closing already supplied the evidence. The shared publication
	// path applies the remaining consumer input and record-assembly gates.
	records, closeFault, fatal := publishEligible(ctx, g.id, leg, order, eligible, declarationsFor(g), reasons)
	if fatal != nil {
		return nil, nil, "", fatal
	}
	if closeFault != nil {
		return nil, nil, reasonSourceFailed.with(closeFault.Error()).String(), nil
	}
	return records, reasons, "", nil
}

// degrade ends the run's further publication with the named cause and
// releases every leg still held: nothing publishes after a degrade, so
// no analysis view is read again and none is carried to the run's end.
func (r *WitnessRecorder) degrade(reason string) {
	r.degraded = reason
	for _, g := range r.groups {
		for pkg := range g.legs {
			g.release(pkg)
		}
	}
}

// release drops one package's leg and, once every leg of the group has
// released, the group's view: the memory a published package's analysis
// held returns while its siblings still execute.
func (g *captureGroup) release(pkg string) {
	if leg := g.legs[pkg]; leg != nil {
		leg.release()
	}
	if legsReleased(g.legs) {
		g.view = nil
	}
}

func (g *captureGroup) subsetForSubjects(subjects []gofresh.Subject) *captureGroup {
	if g == nil {
		return nil
	}
	sub := *g
	sub.view = nil
	sub.legs = nil
	sub.packages = make(map[string]*groupPackage, len(subjects))
	for _, s := range subjects {
		if p, ok := g.packages[s.Package]; ok {
			sub.packages[s.Package] = p
		}
	}
	return &sub
}

func compactRegs(regs []verify.Registration) []verify.Registration {
	out := regs[:0]
	for i, reg := range regs {
		if i == 0 || reg != regs[i-1] {
			out = append(out, reg)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ExecutePolicyWitnessed executes the accepted policy once and derives
// suite health and witness evidence from that same execution
// (SuiteHealthy over the report, and the returned test run): freshness
// fingerprints are captured before execution so published records pin the
// tree that compiled the binaries, and per-test records publish only
// after source and runtime producer validation. Caller cancellation
// anywhere discards the whole result.
func ExecutePolicyWitnessed(ctx context.Context, pc *Capture, seeding verify.WitnessSeeding) (*stipulatorv1.ExecutionReport, *verify.TestRun, error) {
	rep := progress.FromContext(ctx)
	if rep == nil {
		rep = progress.New(nil)
		ctx = progress.NewContext(ctx, rep)
	}
	// The pre-execution recorder derives the capture's discovered leg:
	// discovery-phase work.
	rep.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	recorder, err := NewWitnessRecorder(ctx, pc, seeding)
	if err != nil {
		// A toolchain-provenance refusal aborts before the suite runs:
		// the refused frontend discovered and selected it
		// (REQ-evidence-toolchain-provenance).
		return nil, nil, err
	}
	defer recorder.Close()
	proofs := processProofs{}
	for _, g := range recorder.groups {
		proofs.add(g, g.legs)
	}
	report, _, err := executePolicy(ctx, pc, func(invocation string, unit packageUnit) error {
		return recorder.packageCompleted(ctx, invocation, unit)
	}, proofs)
	if err != nil {
		return nil, nil, err
	}
	// Producer validation and publication judge the evidence the run
	// produced: verification-phase work.
	rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	tr, err := recorder.Derive(ctx, report)
	if err != nil {
		return nil, nil, err
	}
	// The witness-eligible selection boundary holds on this form
	// exactly as on the selective one — one classification on the
	// discovery, the run's outcomes as the evidence and the report's
	// invocation health as the cause (REQ-check-witness-selection). The
	// universe is the one the execution itself selected against — the
	// capture's, discovered before the suite ran: a test the run could
	// only have created mid-execution predates no policy, and a source
	// edit mid-run discards the run's records anyway. A universe fault
	// degrades silently: without it only the invocations' own
	// obligations classify.
	d, err := pc.discover(ctx) // the held leg: discovered before the suite ran
	if err != nil {
		return nil, nil, err
	}
	universe, _ := pc.ObligationUniverse(ctx)
	health := reportPackageHealth(report)
	classes := d.classifyWitnesses(universe, executedTopPackages(report), tr.Outcomes, func(invocation, pkg string) (string, bool) {
		disposition, ok := health[invPkgKey(invocation, pkg)]
		if !ok {
			return "", false
		}
		return dispositionCause(invocation, pkg, disposition, packageReason(report.GetDiagnostics(), invocation, pkg, disposition)), true
	})
	tr.OutsideSubjects = classes.outside
	tr.OutsidePolicy = len(classes.outside)
	tr.NoOutcome = classes.noOutcome
	published, _, _ := recorder.publish()
	tr.InvocationWitnesses = buildInvocationWitnessAccounts(
		d,
		report.GetTests(),
		nil,
		publishedByInvocation(d.groups, published),
		tr.NoOutcome,
		nil,
	)
	tr.Timing = rep.TimingDiagnostics()
	report.SetTiming(tr.Timing)
	return report, tr, nil
}

func publishedByInvocation(groups []*captureGroup, published []witnesscache.Record) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, rec := range published {
		key := rec.Package + "." + rec.Test
		for _, g := range groups {
			if g.id != rec.Group {
				continue
			}
			if inv := g.selectingInvocation(rec.Package); inv != "" {
				if out[inv] == nil {
					out[inv] = map[string]bool{}
				}
				out[inv][key] = true
			}
		}
	}
	return out
}

func buildInvocationWitnessAccounts(
	d *policyDiscovery,
	rows []*stipulatorv1.TestResult,
	servedByInv map[string]map[string]bool,
	publishedByInv map[string]map[string]bool,
	noOutcome map[string]string,
	scopeSkipped map[string]bool,
) []*stipulatorv1.InvocationWitnessAccount {
	if d == nil {
		return nil
	}
	executedByInv := map[string]map[string]bool{}
	for _, row := range rows {
		inv := row.GetProducer().GetInvocation()
		if inv == "" {
			continue
		}
		key, ok := executedTopKey(row.GetPackage(), row.GetTest())
		if !ok {
			continue
		}
		if executedByInv[inv] == nil {
			executedByInv[inv] = map[string]bool{}
		}
		executedByInv[inv][key] = true
	}
	out := make([]*stipulatorv1.InvocationWitnessAccount, 0, len(d.invocations))
	for _, ic := range d.invocations {
		inv := ic.n.Name
		served := servedByInv[inv]
		execSet := executedByInv[inv]
		pubSet := publishedByInv[inv]
		uncacheable := 0
		for key := range execSet {
			if !pubSet[key] && !served[key] {
				uncacheable++
			}
		}
		noOutCount := 0
		if ic.n.WitnessEligible() {
			seen := map[string]bool{}
			for _, o := range ic.obligations {
				if o.Kind != ObligationTest && o.Kind != ObligationFuzz {
					continue
				}
				key := o.Package + "." + o.Name
				if seen[key] || scopeSkipped[key] {
					continue
				}
				seen[key] = true
				if _, missing := noOutcome[key]; missing {
					noOutCount++
				}
			}
		}
		acc := &stipulatorv1.InvocationWitnessAccount{}
		acc.SetInvocation(inv)
		acc.SetWitnessEligible(ic.n.WitnessEligible())
		acc.SetPlainWitness(ic.n.PlainWitness)
		acc.SetServed(int32(len(served)))
		acc.SetExecuted(int32(len(execSet)))
		acc.SetUncacheable(int32(uncacheable))
		acc.SetNoOutcome(int32(noOutCount))
		out = append(out, acc)
	}
	return out
}

// reportPackageHealth indexes the report's package dispositions by
// invocation and package.
func reportPackageHealth(report *stipulatorv1.ExecutionReport) map[string]stipulatorv1.HealthDisposition {
	health := map[string]stipulatorv1.HealthDisposition{}
	for _, h := range report.GetInvocations() {
		for _, p := range h.GetPackages() {
			health[invPkgKey(h.GetInvocation(), p.GetPackage())] = p.GetDisposition()
		}
	}
	return health
}

// witnessClasses is one classification of every expected witness
// subject: outside when no eligible invocation selects it; no witness
// outcome, with the execution-layer cause, when the eligible selection
// covers it but the run granted it nothing (REQ-check-witness-selection).
type witnessClasses struct {
	outside   map[string]bool
	noOutcome map[string]string
}

// classifyWitnesses is the one rule both evidence forms answer with. The
// expected subjects are the universe's test and fuzz obligations and
// every invocation's own; an executed subject the universe lacks is
// expected too (executed maps its key to its package). A subject is
// outside when no witness-eligible invocation selects it. An
// eligible-covered subject with no outcome — no result, or a result
// its package's process denied a grant — names its cause: each
// eligible invocation selecting it with the package's disposition
// under that invocation, from causeFor; an invocation with no
// disposition reported it no result. A nil causeFor classes the
// outside set alone — the selective form's pre-run count.
func (d *policyDiscovery) classifyWitnesses(universe []Obligation, executed map[string]string, outcomes map[string]verify.TestOutcome, causeFor func(invocation, pkg string) (string, bool)) witnessClasses {
	expected := map[string]string{} // key -> package
	for _, o := range universe {
		if o.Kind == ObligationTest || o.Kind == ObligationFuzz {
			expected[o.Package+"."+o.Name] = o.Package
		}
	}
	eligibleBy := map[string][]string{} // key -> the eligible invocations selecting it
	for _, ic := range d.invocations {
		for _, o := range ic.obligations {
			if o.Kind != ObligationTest && o.Kind != ObligationFuzz {
				continue
			}
			key := o.Package + "." + o.Name
			expected[key] = o.Package
			if ic.n.WitnessEligible() {
				eligibleBy[key] = append(eligibleBy[key], ic.n.Name)
			}
		}
	}
	for key, pkg := range executed {
		if _, ok := expected[key]; !ok {
			expected[key] = pkg
		}
	}
	classes := witnessClasses{outside: map[string]bool{}, noOutcome: map[string]string{}}
	for key, pkg := range expected {
		invocations := eligibleBy[key]
		if len(invocations) == 0 {
			classes.outside[key] = true
			continue
		}
		if causeFor == nil {
			continue
		}
		if _, ok := outcomes[key]; ok {
			continue
		}
		var causes []string
		for _, inv := range invocations {
			if cause, ok := causeFor(inv, pkg); ok {
				causes = append(causes, cause)
			} else {
				causes = append(causes, fmt.Sprintf("invocation %s reported no result for package %s", inv, pkg))
			}
		}
		sort.Strings(causes)
		classes.noOutcome[key] = strings.Join(causes, "; ")
	}
	return classes
}

// executedTopKeys is the executed top-level witness-subject key set —
// "pkg.TopLevelTest" per report row, examples excluded by the
// toolchain's own dispatch rule, the same rule Ran counts by: the
// attribution map and the Ran count must never desynchronize.
func executedTopKeys(report *stipulatorv1.ExecutionReport) map[string]bool {
	keys := map[string]bool{}
	for key := range executedTopPackages(report) {
		keys[key] = true
	}
	return keys
}

// executedTopPackages is executedTopKeys with each key's package, as
// the row states it.
func executedTopPackages(report *stipulatorv1.ExecutionReport) map[string]string {
	keys := map[string]string{}
	for _, row := range report.GetTests() {
		if key, ok := executedTopKey(row.GetPackage(), row.GetTest()); ok {
			keys[key] = row.GetPackage()
		}
	}
	return keys
}

// dispositionCause spells one package's disposition under its
// invocation as an execution-layer cause: "invocation race: package p
// timeout", or, healthy, that the invocation's process granted the
// subject nothing.
func dispositionCause(invocation, pkg string, disposition stipulatorv1.HealthDisposition, reason string) string {
	if disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		return fmt.Sprintf("invocation %s: package %s healthy, no result for it", invocation, pkg)
	}
	text := strings.ToLower(strings.TrimPrefix(disposition.String(), "HEALTH_DISPOSITION_"))
	cause := fmt.Sprintf("invocation %s: package %s %s", invocation, pkg, strings.ReplaceAll(text, "_", " "))
	if reason != "" {
		cause += ": " + reason
	}
	return cause
}
