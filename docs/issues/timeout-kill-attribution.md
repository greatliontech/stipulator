# A harness-timeout kill is reported as the running test's failure

Under the completed-observation semantics (evidence.md's record-identity
clause: an execution bound can only prevent a measurement from
completing), a run that exhausts `-test.timeout` or the invocation
envelope's timeout is an envelope fact — the budget ran out. The
executor does not classify it: the timeout panic reds the running test
and its package exactly like a genuine failure, so diagnostics
attribute budget exhaustion to whichever test held the deadline
(`execute.go`'s stream ingest; `DeriveTestRun` records the red
regardless). Suite health SHOULD be red on that run — the run did fail
— but the per-test attribution misleads: the named test is not broken,
the budget is too small, and nothing in the output says which.

No evidence corruption: an unhealthy package publishes nothing, so the
kill never poisons the witness store, and previously completed
observations keep serving (the cold-red/warm-green asymmetry across a
budget edit is the contract working, not a defect). The gap is purely
diagnostic — classify the timeout panic (its output shape is
recognizable: "panic: test timed out after ...") and say "execution
budget exhausted" with the bound, instead of a bare test failure.

Lands: cross-tool train chunk 114 (stipulator runner-environment
inspectability — the executor's stream-ingest diagnostics are its
scope).
