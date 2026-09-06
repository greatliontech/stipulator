# Executor diagnostics: budget rendering, residue classification, and the abort predicate share one shape

`internal/backends/golang/execute.go` carries three per-run judgments
over the same inputs (the stream state, the bounded stderr, the wait
error, the declared binary bound): `declaredBinaryBound` renders the
budget the run was held to, `cutoffResidue` classifies what a cut-off
run leaves behind, and `isAbortOutput` decides whether the residue is
an abort. Each walks the stream and stderr on its own terms, so a new
run-ending cause (a fourth kind of cut-off, say) is added three times
with three vocabularies. One per-run diagnosis — a single walk
producing the bound, the residue class, and the abort verdict as one
value that `classifyRun` consumes — would collapse them; the outcome
classes `classifyRun` assigns and the attribution REQ-policy-budget-attribution
requires are the invariants to preserve.

Lands: with the next change set touching the executor's run
classification (`classifyRun` or the helpers above).
