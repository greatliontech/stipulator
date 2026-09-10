package golang

import (
	"slices"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
)

// producerCandidate is one process that may have granted a subject's
// outcome: whether the run disposed it healthy — the process's own
// disposition on the selective form, the package's disposition under
// its covering invocation on the full form, where one process is the
// package — its owned observation (nil, or incomplete, when the
// testlog flush is unproven), and every row it produced for the
// subject's package (none when it died before its first terminal
// event). The selective form offers every process of the group's own
// invocations that ran the subject, in first-seen order; the full form
// offers the one process under the package's covering invocation.
type producerCandidate struct {
	healthy bool
	obs     *ProcessObservation
	rows    []*stipulatorv1.TestResult
}

// The one reason vocabulary of the per-subject publish judgment, on
// both forms (REQ-evidence-witness-freshness's diagnosable set: every
// unpublished subject names the leg that refused it).
const (
	reasonNoProducingLeg    = "two invocations of one capture group select the package; no single producing leg"
	reasonNoFingerprint     = "pre-execution fingerprint capture failed"
	reasonNoTerminalEvent   = "no terminal event from the producing process"
	reasonFlushUnproven     = "producing process's testlog flush unproven"
	reasonProducerUnhealthy = "producing package disposed unhealthy"
	reasonNoHealthyOutcome  = "no healthy outcome for the subject"
)

// judgeSubject is the per-subject publish judgment both forms apply, in
// one order: the subject's own refusals first — a subject serving
// refuses by contract keeps its classifier's reason, a subject whose
// pre-execution fingerprint was not captured cannot be keyed — then the
// candidates in order: the first healthy process that produced rows,
// proved its flush, and whose rows fold to one outcome word for the
// subject grants it, its material returned ready for the publication
// ladder — a failed row inside a healthy process, or no row for the
// subject, is a contradiction the record refuses rather than caching
// either side of. The reason names the strongest refusal seen: an
// unproven flush over an unhealthy process (unless a healthy one
// contradicted) over a missing outcome; a process that died before
// its first terminal event, or no candidate at all, is a missing
// terminal event. The solo flag is the process's own fact — its rows
// hold the subject's top-level test and no other — on both forms.
func judgeSubject(s gofresh.Subject, neverServe string, refused, captured bool, candidates []producerCandidate) (*pubSubject, string) {
	if refused {
		return nil, neverServe
	}
	if !captured {
		return nil, reasonNoFingerprint
	}
	sawUnhealthy, sawUnproven, sawContradiction := false, false, false
	for _, c := range candidates {
		if !c.healthy {
			sawUnhealthy = true
			continue
		}
		if len(c.rows) == 0 {
			continue
		}
		if c.obs == nil || c.obs.Wire.GetCompleted() == nil {
			sawUnproven = true
			continue
		}
		ps := &pubSubject{obs: c.obs, outcomes: map[string]string{}}
		tops := map[string]bool{}
		contradicted := false
		for _, row := range c.rows {
			test := row.GetTest()
			tops[topLevel(test)] = true
			if test != s.Symbol && !strings.HasPrefix(test, s.Symbol+"/") {
				continue
			}
			var word string
			switch row.GetOutcome() {
			case stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED:
				word = "passed"
			case stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED:
				word = "skipped"
			default:
				contradicted = true
			}
			ps.outcomes[row.GetPackage()+"."+test] = word
			for _, req := range row.GetRegistrations() {
				ps.regs = append(ps.regs, verify.Registration{Package: s.Package, Test: test, Requirement: req})
			}
		}
		if contradicted || ps.outcomes[s.Package+"."+s.Symbol] == "" {
			sawContradiction = true
			continue
		}
		ps.solo = len(tops) == 1 && tops[s.Symbol]
		return ps, ""
	}
	switch {
	case sawUnproven:
		return nil, reasonFlushUnproven
	case sawUnhealthy && !sawContradiction:
		return nil, reasonProducerUnhealthy
	case sawContradiction:
		return nil, reasonNoHealthyOutcome
	default:
		return nil, reasonNoTerminalEvent
	}
}

// producersOf lists, in first-seen order, every process of the group's
// own invocations the merge holds rows of the subject for — the
// whole-package process, then the isolation pass's solo one — as the
// one judgment's candidates, each with every row it produced for the
// package (REQ-policy-attribution). A process of another group's
// invocation sharing the package is no candidate: a group publishes
// under its own witness class, so only its own legs can grant.
func producersOf(s gofresh.Subject, g *captureGroup, m *execMerge) []producerCandidate {
	var order []producerKey
	own := map[producerKey]bool{}
	rows := map[producerKey][]*stipulatorv1.TestResult{}
	for _, row := range m.rows {
		if row.GetPackage() != s.Package {
			continue
		}
		k := keyOfProducer(row.GetProducer())
		if _, seen := rows[k]; !seen {
			rows[k] = nil
			own[k] = slices.Contains(g.invs, row.GetProducer().GetInvocation())
		}
		if !own[k] {
			continue
		}
		rows[k] = append(rows[k], row)
		if test := row.GetTest(); test == s.Symbol || strings.HasPrefix(test, s.Symbol+"/") {
			if !slices.Contains(order, k) {
				order = append(order, k)
			}
		}
	}
	candidates := make([]producerCandidate, 0, len(order))
	for _, k := range order {
		candidates = append(candidates, producerCandidate{
			healthy: m.disp[k] == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
			obs:     m.obs[k],
			rows:    rows[k],
		})
	}
	return candidates
}

// executedTopKey is the executed-subject key of one result row on both
// forms — the package-qualified top-level test — and false for an
// example: examples execute too but never enter the freshness cache,
// so counting them would permanently inflate the uncacheable number.
// The Example prefix is the toolchain's own dispatch rule, not a
// heuristic.
func executedTopKey(pkg, test string) (string, bool) {
	top := topLevel(test)
	if strings.HasPrefix(top, "Example") {
		return "", false
	}
	return pkg + "." + top, true
}
