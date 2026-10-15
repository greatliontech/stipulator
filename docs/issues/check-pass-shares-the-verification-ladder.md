# One evaluation entry and non-measuring inspections

Lands: tooling-redesign 7.5

`internal/check/check.go` assembles capture, operation symbols and served
backends separately from `internal/verifyrun/verifyrun.go`. The latter's
`Run`, `Scoped` and `Gaps` still call `RunTests`; judging readers can therefore
measure. The explicit contradicted gaps on REQ-evidence-inspection,
REQ-gap-list and REQ-gap-resolved-pruned record this remaining mismatch.

The obligation is one explicit evaluation entry with non-measuring readers,
not folding Check into the existing measuring reader and calling it fixed.
Share preparation where its authority and lifetime match, preserve check's
scoped witness tiers and suite-health result, and define current/stale/missing,
NoOutcome and partial accounts for both faces. Unknown evidence must neither
grant a pass nor authorize evidence-based deletion. Deliberately version
defaults, exits, MCP fields and replacement of no-test controls. Close the
contradicted gaps only after the read paths and deletion controls enforce
those rules.

Secondary constituent: 5.3 owns durable-prefix and error-return accounts;
consolidation must preserve them rather than reconstructing them in readers.
