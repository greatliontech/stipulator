package golang

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// Witness derivation turns one in-memory execution report into the
// evidence view binding verification consumes: suite health and witness
// outcomes both derive from the same execution, never from a second run
// and never from a cache. The gating is deliberately asymmetric. A pass
// grants a witness only when its producing package disposed healthy
// inside its producing invocation and that invocation ran under the race
// detector — a red suite never yields green evidence, and a non-race
// invocation contributes suite health but no Go witness. A failure is
// surfaced regardless: red is a fact about the tree whatever the rigor of
// the run that saw it. Health is computed from invocation dispositions
// alone — the witness cache is not an input to any health or outcome here,
// so a cached green outcome structurally cannot satisfy package, command,
// or suite health. The cache appears only on the producer side: after a
// healthy race execution, per-test freshness records are published for
// later freshness-serving consumers, each carrying its own producing
// process's runtime observation and only surviving source and runtime
// producer validation.

// SuiteHealthy derives suite health from an execution report: healthy
// exactly when the report carries at least one invocation and every
// invocation's terminal disposition is healthy. Invocation dispositions
// already aggregate their packages, and they are the only health source —
// no served or cached outcome can reach this judgment.
func SuiteHealthy(report *stipulatorv1.ExecutionReport) bool {
	invocations := report.GetInvocations()
	if len(invocations) == 0 {
		return false
	}
	for _, h := range invocations {
		if h.GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
			return false
		}
	}
	return true
}

// invocationFacts indexes the per-invocation facts outcome gating needs:
// the race rigor of each invocation and the terminal disposition of each
// package within it.
type invocationFacts struct {
	race       map[string]bool
	healthyPkg map[string]bool
}

func indexInvocations(report *stipulatorv1.ExecutionReport) invocationFacts {
	f := invocationFacts{race: map[string]bool{}, healthyPkg: map[string]bool{}}
	for _, h := range report.GetInvocations() {
		f.race[h.GetInvocation()] = h.GetGo().GetRace()
		for _, p := range h.GetPackages() {
			if p.GetDisposition() == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
				f.healthyPkg[h.GetInvocation()+"\x00"+p.GetPackage()] = true
			}
		}
	}
	return f
}

// DeriveTestRun derives the witness-evidence view of one execution report
// for binding verification. A passing result becomes a witness outcome
// only when its producing package disposed healthy inside its producing
// invocation and that invocation ran under the race detector; a pass that
// fails either gate records no outcome at all, so a bound test in that
// position reads as unwitnessed. Failed and skipped results are recorded
// regardless — a failure is a fact whatever produced it, and a skip
// grants nothing without reading as broken. When one test name carries
// several results (an invocation with -count above one, or the same
// package under two invocations), the worst outcome wins, so a single
// red occurrence is never papered over by a green sibling. Runtime
// registrations are carried for every result — cross-checking them
// against the binding store is verification's judgment — and test-scoped
// failure diagnostics ride along so a red witness is diagnosable from the
// run that saw it.
func DeriveTestRun(report *stipulatorv1.ExecutionReport) *verify.TestRun {
	facts := indexInvocations(report)
	// Every witness outcome this derivation grants comes from a
	// race-enabled invocation by construction, so the run's witness rigor
	// attribute is race-enabled; results that never become witnesses
	// carry no rigor claim.
	tr := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}, RaceEnabled: true}
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
	ranTop := map[string]bool{}
	for _, row := range report.GetTests() {
		pkg, test := row.GetPackage(), row.GetTest()
		key := pkg + "." + test
		inv := row.GetProducer().GetInvocation()
		outcome := verify.TestNotRun
		switch row.GetOutcome() {
		case stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED:
			outcome = verify.TestFailed
		case stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED:
			outcome = verify.TestSkipped
		case stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED:
			if facts.healthyPkg[inv+"\x00"+pkg] && facts.race[inv] {
				outcome = verify.TestPassed
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
		// Ran counts executed top-level tests and fuzz replays; examples
		// execute too but never enter the freshness cache, so counting
		// them would permanently inflate the uncacheable number. The
		// Example prefix is the toolchain's own dispatch rule, not a
		// heuristic.
		if top := topLevel(test); !strings.HasPrefix(top, "Example") {
			ranTop[pkg+"."+top] = true
		}
	}
	tr.Ran = len(ranTop)
	for _, d := range report.GetDiagnostics() {
		if d.GetTest() == "" {
			continue
		}
		if tr.Failures == nil {
			tr.Failures = map[string]string{}
		}
		// One test can fail more than once in a run (-count above one, two
		// invocations); every occurrence's output is diagnosis material.
		key := d.GetPackage() + "." + d.GetTest()
		if prev, ok := tr.Failures[key]; ok {
			tr.Failures[key] = prev + "\n" + d.GetOutput()
		} else {
			tr.Failures[key] = d.GetOutput()
		}
	}
	sortRegs(tr)
	return tr
}

// captureGroup is one freshness-capture configuration class: every
// race-enabled invocation whose closure-shaping configuration (build tags
// and normalized environment) is identical shares one analysis view, so
// a fingerprint is always captured under the same build selection its
// test executed under.
type captureGroup struct {
	tags []string
	env  []string
	// pkgInv names the one invocation of this group selecting each
	// package; a package two invocations select never publishes, because
	// its record would have no single producing invocation.
	pkgInv map[string]string
	// ambiguous marks packages selected by more than one invocation
	// within the group.
	ambiguous map[string]bool
	// tests holds each package's expected witness set: its named Test
	// functions and fuzz targets.
	tests map[string][]string
	// solo marks packages whose whole-package process runs exactly one
	// top-level runnable (one test or fuzz target and nothing else,
	// executable examples included in the count): only such a process can
	// carry an observation-completeness proof, because a sibling test
	// could contribute unrecorded process state to the subject's outcome.
	solo map[string]bool
	// view and fps are the pre-execution captures: fingerprints must pin
	// the tree that compiles the binaries, so capturing after execution
	// would let a mid-run edit publish pre-edit outcomes under a
	// post-edit hash — a spurious reuse. Captured before, the same
	// interleaving reads stale: the safe direction.
	view *gofresh.View
	fps  map[gofresh.Subject]gofresh.Fingerprint
	// observed carries the observation-completeness proof view for the
	// group's solo candidates, captured before execution and revalidated
	// after.
	observed    *gofresh.View
	observedFPs map[gofresh.Subject]gofresh.Fingerprint
	candidates  []gofresh.Subject
}

// WitnessRecorder is the producer side of witness freshness under the
// accepted policy: it captures per-test fingerprints before the policy
// executes and publishes witness-cache records from the execution report
// after, once source and runtime producer validation succeed. It is a
// cache producer only — health and witness evidence never depend on it,
// and any fault on this path degrades to publishing nothing while the
// derivation's evidence stands (the cache saves work, it never blocks or
// weakens witnessing).
type WitnessRecorder struct {
	dir      string
	degraded string
	groups   []*captureGroup
}

// NewWitnessRecorder prepares freshness publication for one execution of
// the accepted policy: it must be called before the policy executes, so
// the captured fingerprints pin the tree the execution compiles. Only
// race-enabled Go invocations are captured — a non-race invocation grants
// no witness evidence, so nothing it produces may enter the cache a
// freshness-serving run would grant evidence from. A fault while
// preparing disables publication and is reported through the derived
// run's degraded reason, never as an error: publication is optimization,
// not correctness.
func NewWitnessRecorder(ctx context.Context, dir string, p *stipulatorv1.TestPolicy) *WitnessRecorder {
	r := &WitnessRecorder{dir: dir}
	degrade := func(err error) *WitnessRecorder {
		r.degraded = err.Error()
		r.groups = nil
		return r
	}
	// A package selected by more than one invocation — race or not, in any
	// group — can never publish: its record would have no single producing
	// invocation. Counting selections across the whole policy first keeps
	// such packages out of every capture, so their guaranteed
	// ineligibility can never strip the observation-proof leg from a
	// group's publishable candidates.
	type invEntry struct {
		n           *NormalizedInvocation
		obligations []Obligation
	}
	var entries []invEntry
	globalCount := map[string]int{}
	for _, inv := range p.GetInvocations() {
		if inv.GetGo() == nil {
			continue
		}
		n, err := NormalizeInvocation(ctx, dir, inv)
		if err != nil {
			return degrade(err)
		}
		obligations, err := DiscoverInvocation(ctx, n)
		if err != nil {
			return degrade(err)
		}
		selected := map[string]bool{}
		for _, o := range obligations {
			selected[o.Package] = true
		}
		for pkg := range selected {
			globalCount[pkg]++
		}
		if n.Race {
			entries = append(entries, invEntry{n: n, obligations: obligations})
		}
	}
	byKey := map[string]*captureGroup{}
	var keys []string
	for _, e := range entries {
		n := e.n
		tests := map[string][]string{}
		runnables := map[string]int{}
		for _, o := range e.obligations {
			switch o.Kind {
			case ObligationTest, ObligationFuzz:
				tests[o.Package] = append(tests[o.Package], o.Name)
				runnables[o.Package]++
			case ObligationExample:
				runnables[o.Package]++
			}
		}
		key := strings.Join(n.Tags, ",") + "\x00" + strings.Join(n.Env, "\x01")
		g := byKey[key]
		if g == nil {
			g = &captureGroup{
				tags:      n.Tags,
				env:       n.Env,
				pkgInv:    map[string]string{},
				ambiguous: map[string]bool{},
				tests:     map[string][]string{},
				solo:      map[string]bool{},
			}
			byKey[key] = g
			keys = append(keys, key)
		}
		for pkg, names := range tests {
			if prev, taken := g.pkgInv[pkg]; taken && prev != n.Name {
				g.ambiguous[pkg] = true
				continue
			}
			g.pkgInv[pkg] = n.Name
			g.tests[pkg] = names
			g.solo[pkg] = runnables[pkg] == 1 && len(names) == 1
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		g := byKey[key]
		var subjects []gofresh.Subject
		for pkg, names := range g.tests {
			if globalCount[pkg] != 1 {
				continue
			}
			for _, name := range names {
				subjects = append(subjects, gofresh.Subject{Package: pkg, Symbol: name})
			}
		}
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
		flags := []string{"-race"}
		if len(g.tags) > 0 {
			flags = append(flags, "-tags="+strings.Join(g.tags, ","))
		}
		engine, err := gofresh.New(
			gofresh.WithDir(dir),
			gofresh.WithBuildFlags(flags...),
			gofresh.WithEnv(g.env...),
		)
		if err != nil {
			return degrade(err)
		}
		view, err := engine.NewView(ctx, subjects, dir)
		if err != nil {
			return degrade(err)
		}
		g.view = view
		g.fps = map[gofresh.Subject]gofresh.Fingerprint{}
		for _, s := range subjects {
			// A subject that fails to fingerprint simply stays
			// unpublishable; its execution and evidence are untouched.
			if fp, err := view.Capture(s); err == nil {
				g.fps[s] = fp
			}
		}
		for _, s := range subjects {
			fp, captured := g.fps[s]
			if captured && g.solo[s.Package] && fp.PurityAssertion == "" {
				g.candidates = append(g.candidates, s)
			}
		}
		g.observed, g.observedFPs = observedView(ctx, engine, g.candidates, dir)
		r.groups = append(r.groups, g)
	}
	// Release transient package-loading memory before the caller spawns
	// race-instrumented builds; the views stay alive for post-execution
	// producer validation.
	debug.FreeOSMemory()
	return r
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
func (r *WitnessRecorder) Derive(ctx context.Context, report *stipulatorv1.ExecutionReport, observations []*ProcessObservation) (*verify.TestRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tr := DeriveTestRun(report)
	published, degraded, err := r.publish(ctx, report, observations)
	if err != nil {
		return nil, err
	}
	switch {
	case degraded != "":
		tr.Degraded = degraded
		tr.Uncached = tr.Ran
	case len(r.groups) == 0:
		// Nothing was capturable (no race-enabled invocation, or no
		// expected tests): every executed test is uncacheable and the
		// existing cache is left alone.
		tr.Uncached = tr.Ran
	default:
		if tr.Ran > len(published) {
			tr.Uncached = tr.Ran - len(published)
		}
		// Publication replaces exactly what this execution touched. A prior
		// record whose test produced no row this run — a shadowed sibling, a
		// package this policy never selected — was legitimately published by
		// an earlier run (a selective isolated run among them) and is
		// retained: discarding it would silently shrink the cache for work
		// this run never re-did. Retention is safe because serving
		// revalidates every record's fingerprint, so a retained record
		// whose inputs moved simply re-runs; a deleted test's record
		// lingers here as dead weight until a serving run's own save
		// drops it — cost, never correctness.
		touched := map[string]bool{}
		for _, row := range report.GetTests() {
			touched[row.GetPackage()+"."+topLevel(row.GetTest())] = true
		}
		for _, rec := range witnesscache.Load(r.dir) {
			if !touched[rec.Package+"."+rec.Test] {
				published = append(published, rec)
			}
		}
		sort.Slice(published, func(i, j int) bool {
			a, b := published[i], published[j]
			if a.Package != b.Package {
				return a.Package < b.Package
			}
			return a.Test < b.Test
		})
		if err := witnesscache.EnsureIgnored(r.dir); err == nil {
			_ = witnesscache.Save(r.dir, published)
		}
	}
	return tr, nil
}

// publish assembles and validates the freshness records one execution
// report supports. It returns the publishable records, or the degraded
// reason when a fault disabled publication whole; the error return is
// reserved for caller cancellation.
func (r *WitnessRecorder) publish(ctx context.Context, report *stipulatorv1.ExecutionReport, observations []*ProcessObservation) ([]witnesscache.Record, string, error) {
	if r.degraded != "" {
		return nil, r.degraded, nil
	}
	if len(r.groups) == 0 {
		return nil, "", nil
	}
	facts := indexInvocations(report)
	// A package under more than one invocation has no single producing
	// invocation for its record; it executes and witnesses normally but
	// never publishes.
	selectCount := map[string]int{}
	for _, h := range report.GetInvocations() {
		for _, p := range h.GetPackages() {
			selectCount[p.GetPackage()]++
		}
	}
	rowsByInvPkg := map[string][]*stipulatorv1.TestResult{}
	for _, row := range report.GetTests() {
		k := row.GetProducer().GetInvocation() + "\x00" + row.GetPackage()
		rowsByInvPkg[k] = append(rowsByInvPkg[k], row)
	}
	obsByProducer := map[producerKey]*ProcessObservation{}
	for _, o := range observations {
		obsByProducer[keyOfProducer(o.Wire.GetProducer())] = o
	}

	var published []witnesscache.Record
	for _, g := range r.groups {
		records, degraded, err := r.publishGroup(ctx, g, facts, selectCount, rowsByInvPkg, obsByProducer)
		if err != nil || degraded != "" {
			return nil, degraded, err
		}
		published = append(published, records...)
	}
	return published, "", nil
}

// groupSubject is one publishable subject's execution-side material.
type groupSubject struct {
	subject  gofresh.Subject
	obs      *ProcessObservation
	rows     []*stipulatorv1.TestResult
	soloRun  bool
	outcomes map[string]string
	regs     []verify.Registration
}

func (r *WitnessRecorder) publishGroup(ctx context.Context, g *captureGroup, facts invocationFacts, selectCount map[string]int, rowsByInvPkg map[string][]*stipulatorv1.TestResult, obsByProducer map[producerKey]*ProcessObservation) ([]witnesscache.Record, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	// Source producer validation: the analysis view must still describe
	// the tree after execution, or every fingerprint of the group is a
	// hash of a tree the outcomes may not have come from.
	if err := g.view.Validate(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, fmt.Sprintf("source producer validation failed: %v", err), nil
	}

	eligible := map[gofresh.Subject]*groupSubject{}
	var order []gofresh.Subject
	for pkg, names := range g.tests {
		inv, ok := g.pkgInv[pkg]
		if !ok || g.ambiguous[pkg] || selectCount[pkg] != 1 {
			continue
		}
		if !facts.healthyPkg[inv+"\x00"+pkg] {
			continue
		}
		rows := rowsByInvPkg[inv+"\x00"+pkg]
		if len(rows) == 0 {
			continue
		}
		// The executor launches exactly one process per selected package per
		// invocation, so every row under this key shares one producer.
		producer := keyOfProducer(rows[0].GetProducer())
		obs := obsByProducer[producer]
		if obs == nil || obs.Wire.GetCompleted() == nil {
			// The producing process's testlog flush is unproven: its
			// tests execute and witness, they just cannot cache.
			continue
		}
		tops := map[string]bool{}
		for _, row := range rows {
			tops[topLevel(row.GetTest())] = true
		}
		for _, name := range names {
			subject := gofresh.Subject{Package: pkg, Symbol: name}
			if _, captured := g.fps[subject]; !captured {
				continue
			}
			gs := &groupSubject{subject: subject, obs: obs, soloRun: len(tops) == 1 && tops[name]}
			gs.outcomes = map[string]string{}
			contradicted := false
			for _, row := range rows {
				test := row.GetTest()
				if test != name && !strings.HasPrefix(test, name+"/") {
					continue
				}
				var word string
				switch row.GetOutcome() {
				case stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED:
					word = "passed"
				case stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED:
					word = "skipped"
				default:
					// A failed result inside a healthy package is a
					// contradiction; refuse the record rather than cache
					// either side of it.
					contradicted = true
				}
				gs.outcomes[row.GetPackage()+"."+test] = word
				for _, req := range row.GetRegistrations() {
					gs.regs = append(gs.regs, verify.Registration{Package: pkg, Test: test, Requirement: req})
				}
			}
			if contradicted || gs.outcomes[pkg+"."+name] == "" {
				continue
			}
			eligible[subject] = gs
			order = append(order, subject)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		return a.Symbol < b.Symbol
	})

	// Observation-completeness proofs attach only when every candidate of
	// the group can attach: the observed view revalidates as one unit, so
	// a single candidate whose process left no completed observation (or
	// did not run its subject alone) drops the proof leg whole and every
	// candidate falls back to the plain per-package manifest.
	attached := map[gofresh.Subject]gofresh.Fingerprint{}
	attachedValid := map[gofresh.Subject]bool{}
	if g.observed != nil && len(g.observedFPs) == len(g.candidates) {
		complete := true
		for _, subject := range g.candidates {
			gs, ok := eligible[subject]
			if !ok || !gs.soloRun {
				complete = false
				break
			}
			fp, err := g.observed.AttachObservation(subject, g.observedFPs[subject], gs.obs.Runtime)
			if err != nil {
				complete = false
				break
			}
			state, err := runtimeinput.CompletedState(gs.obs.Runtime)
			if err != nil {
				complete = false
				break
			}
			attached[subject] = fp
			attachedValid[subject] = validatedObservation(fp, state)
		}
		if complete && len(g.candidates) > 0 {
			if err := g.observed.ValidateObserved(ctx); err != nil {
				if ctx.Err() != nil {
					return nil, "", ctx.Err()
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
	for _, subject := range order {
		gs := eligible[subject]
		if fp, ok := attached[subject]; ok {
			final[subject] = fp
			continue
		}
		fp := g.fps[subject]
		if fp.ObservationAssertion == "" {
			state, err := runtimeinput.CompletedState(gs.obs.Runtime)
			if err != nil {
				continue
			}
			fp.RuntimeInputs, fp.RuntimeDigest = state.Manifest, state.Digest
		}
		final[subject] = fp
	}

	// Runtime producer validation: each record publishes only when its
	// post-run check returns valid against the current tree. A stale
	// verdict is a mid-run drift of the record's source or runtime inputs
	// — the executed outcome stands, the record is dropped so the next
	// run re-derives it; an unverifiable verdict can never check valid
	// and is dropped the same way. Both are visible as the uncacheable
	// count, never silence.
	verdicts := map[gofresh.Subject]gofresh.Verdict{}
	unvalidated := map[gofresh.Subject]gofresh.Fingerprint{}
	for subject, fp := range final {
		if attachedValid[subject] {
			verdicts[subject] = gofresh.Verdict{Status: gofresh.Valid}
		} else {
			unvalidated[subject] = fp
		}
	}
	checked, err := checkFingerprints(ctx, g.view, unvalidated)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, fmt.Sprintf("runtime producer validation failed: %v", err), nil
	}
	for subject, verdict := range checked {
		verdicts[subject] = verdict
	}

	var records []witnesscache.Record
	for _, subject := range order {
		fp, ok := final[subject]
		if !ok || verdicts[subject].Status != gofresh.Valid {
			continue
		}
		gs := eligible[subject]
		regs := append([]verify.Registration(nil), gs.regs...)
		sort.Slice(regs, func(i, j int) bool {
			a, b := regs[i], regs[j]
			if a.Test != b.Test {
				return a.Test < b.Test
			}
			return a.Requirement < b.Requirement
		})
		records = append(records, witnesscache.Record{
			Package:     subject.Package,
			Test:        subject.Symbol,
			Fingerprint: witnesscache.FromGofresh(fp),
			Outcomes:    gs.outcomes,
			Regs:        compactRegs(regs),
		})
	}
	return records, "", nil
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
func ExecutePolicyWitnessed(ctx context.Context, dir string, p *stipulatorv1.TestPolicy) (*stipulatorv1.ExecutionReport, *verify.TestRun, error) {
	recorder := NewWitnessRecorder(ctx, dir, p)
	report, observations, err := ExecutePolicy(ctx, dir, p)
	if err != nil {
		return nil, nil, err
	}
	tr, err := recorder.Derive(ctx, report, observations)
	if err != nil {
		return nil, nil, err
	}
	return report, tr, nil
}
