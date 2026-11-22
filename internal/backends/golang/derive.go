package golang

import (
	"fmt"

	gofresh "github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// Witness derivation turns one in-memory execution report into the
// evidence view binding verification consumes: suite health and witness
// outcomes both derive from the same execution, never from a second run
// and never from a cache. The gating is deliberately asymmetric. A pass
// grants a witness only when its producing package disposed healthy
// inside a race-enabled or explicitly plain-admitted invocation. A red suite
// never yields green evidence; an unadmitted non-race invocation contributes
// suite health but no Go witness. A failure is
// surfaced regardless: red is a fact about the tree whatever the rigor of
// the run that saw it. Health is computed from invocation dispositions
// alone — the witness cache is not an input to any health or outcome here,
// so a cached green outcome structurally cannot satisfy package, command,
// or suite health. The cache appears only on the producer side: after a
// healthy witness-eligible execution, per-test freshness records are published for
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
	plain      map[string]bool
	healthyPkg map[string]bool
}

func indexInvocations(report *stipulatorv1.ExecutionReport) invocationFacts {
	f := invocationFacts{race: map[string]bool{}, plain: map[string]bool{}, healthyPkg: map[string]bool{}}
	for _, h := range report.GetInvocations() {
		f.race[h.GetInvocation()] = h.GetGo().GetRace()
		f.plain[h.GetInvocation()] = h.GetGo().GetPlainWitness()
		for _, p := range h.GetPackages() {
			if p.GetDisposition() == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
				f.healthyPkg[invPkgKey(h.GetInvocation(), p.GetPackage())] = true
			}
		}
	}
	return f
}

// DeriveTestRun derives the witness-evidence view of one execution report
// for binding verification. A passing result becomes a witness outcome
// only when its producing package disposed healthy inside its producing
// invocation and that invocation ran under the race detector or explicitly
// admitted plain witnesses; a pass that fails either gate records no outcome
// at all, so a bound test in that position reads as unwitnessed. Failed and
// skipped results are recorded
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
	// Witness grants derive from witness-eligible invocations: race legs
	// at the strongest tier, explicit plain-witness admissions at the
	// plain tier with the downgrade recorded per test (PlainWitness);
	// results that never become witnesses carry no rigor claim.
	tr := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}, RaceEnabled: true, PlainWitness: map[string]bool{}}
	grantedRace := map[string]bool{}
	ranTop := map[string]bool{}
	foldTestRows(tr, report.GetTests(), ranTop, grantedRace, nil, func(row *stipulatorv1.TestResult) (bool, bool) {
		inv, pkg := row.GetProducer().GetInvocation(), row.GetPackage()
		if !facts.healthyPkg[invPkgKey(inv, pkg)] || (!facts.race[inv] && !facts.plain[inv]) {
			return false, false
		}
		return true, !facts.race[inv]
	})
	tr.Ran = len(ranTop)
	// A key granted by any race leg holds the race tier even when a plain
	// leg also passed it; only keys granted exclusively at the plain tier
	// carry the recorded downgrade.
	for key, outcome := range tr.Outcomes {
		if outcome == verify.TestPassed && !grantedRace[key] {
			tr.PlainWitness[key] = true
		}
	}
	foldTestDiagnostics(tr, report.GetDiagnostics())
	sortRegs(tr)
	tr.Timing = report.GetTiming()
	return tr
}

// foldTestRows folds raw TestResult rows into tr by worst-outcome rank,
// recording runtime registrations and non-example executed top-level subjects
// in ranTop. Passing rows consult grantPass for eligibility and plain-tier
// classification.
func foldTestRows(
	tr *verify.TestRun,
	rows []*stipulatorv1.TestResult,
	ranTop, raceGranted, plainGranted map[string]bool,
	grantPass func(*stipulatorv1.TestResult) (granted, plain bool),
) {
	for _, row := range rows {
		pkg, test := row.GetPackage(), row.GetTest()
		key := pkg + "." + test
		outcome := verify.TestNotRun
		switch row.GetOutcome() {
		case stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED:
			outcome = verify.TestFailed
		case stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED:
			outcome = verify.TestSkipped
		case stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED:
			if granted, plain := grantPass(row); granted {
				outcome = verify.TestPassed
				if plain {
					if plainGranted != nil {
						plainGranted[key] = true
					}
				} else if raceGranted != nil {
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
		// Ran counts executed top-level tests and fuzz replays, never examples.
		if topKey, ok := executedTopKey(pkg, test); ok {
			ranTop[topKey] = true
		}
	}
}

// foldTestDiagnostics folds test-scoped failure diagnostics onto tr.Failures,
// joining repeated occurrences with newlines while leaving package-scoped
// diagnostics on their typed slice alone.
func foldTestDiagnostics(tr *verify.TestRun, diags []*stipulatorv1.FailureDiagnostic) {
	for _, d := range diags {
		if d.GetTest() == "" {
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

// reconcileUncached computes tr.Uncached and tr.UncacheableReasons across both
// the selective witness runner and the health-judged witness recorder:
// executed top-level subjects holding neither published nor served evidence
// count uncacheable, attributed by uncacheableWhy, ambiguousSubjects, or
// fallback (REQ-evidence-witness-freshness-diagnosable).
func reconcileUncached(
	tr *verify.TestRun,
	ranTop map[string]bool,
	published []witnesscache.Record,
	servedKey map[string]bool,
	uncacheableWhy map[gofresh.Subject]uncacheable,
	ambiguousSubjects map[gofresh.Subject]bool,
	fallback uncacheable,
) {
	publishedKey := make(map[string]bool, len(published))
	for _, rec := range published {
		publishedKey[rec.Package+"."+rec.Test] = true
	}
	tr.Uncached = 0
	for key := range ranTop {
		if !publishedKey[key] && !servedKey[key] {
			tr.Uncached++
		}
	}
	if tr.Uncached == 0 && len(uncacheableWhy) == 0 && len(ambiguousSubjects) == 0 {
		return
	}
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
			tr.UncacheableReasons[key] = fallback.String()
		}
	}
	for key := range tr.UncacheableReasons {
		if publishedKey[key] || servedKey[key] {
			delete(tr.UncacheableReasons, key)
		}
	}
	if len(tr.UncacheableReasons) == 0 {
		tr.UncacheableReasons = nil
	}
}

// plural renders a count with its noun, pluralized.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
