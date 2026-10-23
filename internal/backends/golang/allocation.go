package golang

import (
	"fmt"
	"maps"
	"runtime"

	"github.com/greatliontech/gofresh"
)

// Allocation has two inputs with different lifetimes: normalization samples
// the processor budget once, while discovery resolves the execution class's
// permitted packages. Finalizing between discovery and capture grouping avoids
// a circular dependency on freshness: the stale remainder depends on the very
// environment being allocated. A smaller remainder or retry therefore never
// changes that environment. Capture identity observes the finalized width;
// persistent record identity remains policy-declared, with width movement
// judged by fingerprints rather than re-addressing the witness store.

// executionScope is fixed before discovery groups invocations by environment.
// It describes possible execution, not the freshness-dependent stale remainder.
type executionScope struct {
	full     bool
	subjects map[gofresh.Subject]bool
}

// ConfigureExecution selects the operation's execution class before any reader
// discovers the capture. The default is whole-tree witness-only execution. A
// non-nil scope restricts witness execution, including drift retries, while
// leaving discovery and serving whole-tree. Full execution takes no scope.
// Repeating the same selection is harmless; changing it after discovery would
// invalidate the already frozen environments and is refused.
func (pc *Capture) ConfigureExecution(full bool, scope map[gofresh.Subject]bool) error {
	if full && scope != nil {
		return fmt.Errorf("full execution cannot carry a witness scope")
	}
	next := executionScope{full: full, subjects: maps.Clone(scope)}
	if pc.discovered.done && pc.discovered.err == nil && (pc.execution.full != full || (pc.execution.subjects == nil) != (scope == nil) || !maps.Equal(pc.execution.subjects, scope)) {
		return fmt.Errorf("execution class and scope must be selected before policy discovery")
	}
	pc.execution = next
	return nil
}

func (s executionScope) permits(subject gofresh.Subject) bool {
	return s.subjects == nil || s.subjects[subject]
}

// selectsWitness includes eligible invocations and the ineligible shared legs
// whose failures still count. Allocation and every-run selection share it.
func (s executionScope) selectsWitness(n *NormalizedInvocation, counts map[string]int, o Obligation) bool {
	return (o.Kind == ObligationTest || o.Kind == ObligationFuzz) &&
		(n.WitnessEligible() || counts[o.Package] > 1) &&
		s.permits(gofresh.Subject{Package: o.Package, Symbol: o.Name})
}

// packageCount follows the same eligibility boundary as witness execution:
// eligible legs, including ambiguous ones, and shared packages' ineligible legs.
// Health execution includes every package obligation, even without a witness.
func (s executionScope) packageCount(ic invocationCapture, globalCount map[string]int) int {
	packages := map[string]bool{}
	for _, o := range ic.obligations {
		if s.full {
			if o.Kind == ObligationPackage {
				packages[o.Package] = true
			}
			continue
		}
		if s.selectsWitness(ic.n, globalCount, o) {
			packages[o.Package] = true
		}
	}
	return len(packages)
}

// retrySubjects is applied at retry selection, not merely when the initial
// drift list is collected. Invalid out-of-scope serves are withheld and marked
// scope-skipped; a later retry request cannot enlarge the execution population.
func (s executionScope) retrySubjects(subjects []gofresh.Subject, skipped map[string]bool) []gofresh.Subject {
	allowed := make([]gofresh.Subject, 0, len(subjects))
	for _, subject := range subjects {
		if s.permits(subject) {
			allowed = append(allowed, subject)
		} else {
			skipped[subject.Package+"."+subject.Symbol] = true
		}
	}
	return allowed
}

// processorBudgetOf uses the one normalization-time sample. The fallback is
// only for hand-built invocations; allocation freezes it before their first use.
func processorBudgetOf(n *NormalizedInvocation) int {
	if n.processorBudget > 0 {
		return n.processorBudget
	}
	return runtime.GOMAXPROCS(0)
}

// freezeWitnessAllocation finishes the normalized execution environment after
// the permitted package population is known, before any capture or spawn. A
// retry reuses the original allocation even when it selects fewer packages.
func freezeWitnessAllocation(n *NormalizedInvocation, packages int) {
	if n.WitnessEnv != nil {
		return
	}
	n.processorBudget = processorBudgetOf(n)
	// A nonzero bound is an already supplied bound on a hand-built invocation
	// (including the admission tests' narrower gate); normalization leaves it
	// unset, and the policy path always derives it here.
	if n.SpawnBound == 0 {
		n.SpawnBound = witnessSpawnBound(n.processorBudget, packages)
	}
	n.WitnessEnv = witnessWidthEnv(n)
}
