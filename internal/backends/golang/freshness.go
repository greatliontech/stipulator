package golang

import (
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// topLevel is the top-level test name of a possibly-subtest path.
func topLevel(test string) string {
	if i := strings.Index(test, "/"); i >= 0 {
		return test[:i]
	}
	return test
}

// outcomeFromString maps a cached outcome back to the verify enum; an
// unknown word reads as not-run, which the correlator treats as
// unwitnessed — the conservative direction.
func outcomeFromString(s string) verify.TestOutcome {
	switch s {
	case "passed":
		return verify.TestPassed
	case "failed":
		return verify.TestFailed
	case "skipped":
		return verify.TestSkipped
	}
	return verify.TestNotRun
}

// assembleWitnessRecord builds one publishable witness record: the compartment
// ledger reads from the same view snapshot the fingerprint's compartment hash
// pinned (the inert-growth carve-out's diff base,
// REQ-evidence-witness-freshness-carve-out), and the registrations sort
// canonically. A subject the view cannot ledger returns false — it stays
// unpublishable.
func assembleWitnessRecord(group string, view *witnessSnapshot, s gofresh.Subject, fp gofresh.Fingerprint, outcomes map[string]string, regs []verify.Registration, declarations recordedDeclarations) (witnesscache.Record, bool) {
	ledger, err := view.TestVariantLedger(s)
	if err != nil {
		return witnesscache.Record{}, false
	}
	sorted := append([]verify.Registration(nil), regs...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Test != b.Test {
			return a.Test < b.Test
		}
		return a.Requirement < b.Requirement
	})
	record := witnesscache.Record{
		Group:             group,
		Package:           s.Package,
		Test:              s.Symbol,
		Fingerprint:       fp,
		CompartmentLedger: witnesscache.LedgerFromGofresh(ledger),
		Outcomes:          outcomes,
		Regs:              compactRegs(sorted),
	}
	declarations.recordInto(&record)
	return record, true
}
