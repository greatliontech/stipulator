# The empty-witness-selection guard is masked by an ineligible leg's red

`check.Run` names the catastrophic shape — the witness-eligible
selection covered no expected witness — only when the pass served
nothing, granted no outcome, and expected witnesses sit outside the
eligible selection (REQ-check-witness-selection). The guard keys on
`len(testRun.Outcomes) == 0`, and its comment claims that keying on
granted outcomes keeps non-race legs — which run but can never grant —
from masking the cause. But the ineligible-leg merge keeps FAILED rows
as outcomes (`consumeMergeFailuresOnly`: red is a fact whatever
produced it), so a failing test in a package only ineligible legs
cover enters `Outcomes`, the guard reads a non-empty set, and the
result-level diagnostic stays silent: every affected binding reads as
a bare tree defect, exactly the shape the diagnostic exists to name.

Reproducer: a policy of two plain invocations both selecting one
package (multiply non-race selected, so every subject is outside), one
of whose tests fails; the default check reports no
witness_selection_problem.

Fix shape: the guard keys on granted outcomes — passed or skipped
outcomes from an eligible leg — never on the presence of a failure the
ineligible merge recorded; the run already distinguishes the merges,
so the count is one field on the test run. The witness-selection
tests (TestCheckMultiplyNonRaceSelectedSubjectsAreOutside and
siblings) gain the red-sibling case.

Lands: cross-tool train chunk 142 (gofresh docs/plans/cross-tool-train.md; surfaced adjacent in chunk 141's review).
