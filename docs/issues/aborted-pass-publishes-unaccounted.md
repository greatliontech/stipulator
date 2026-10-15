# A later operation error loses the resolution publication account

Lands: tooling-redesign 5.3

Reported on 2026-10-05 during verification-account work. Resolution
publication now happens at `Served.Quiesce`, before execution; child release
and that timing are already implemented. The remaining fault is the error
return's account, not a request to move publication to deferred close or
suppress independently completed records.

In `internal/verifyrun/verifyrun.go`, `Run` and `Scoped` defer a
`verify.Closer`, but a `RunTests` error returns nil reports before assigning
`ResolutionNotices`. The successful report and `RefuseReport` carry those
notices; this earlier error return does not. `internal/check` also loses the
result on operational errors and cancellation: its record-problem helper
retains ordinary verdicts and policy-record problems, but returns nil for
other errors. The deferred closer can update a discarded local result, and
the MCP check handler discards an error-accompanying result as well.

Carry the publication account through cancellation and later errors on both
faces. Distinguish durable resolution records, witness records and incomplete
work. Pin an error after Quiesce on both check and verifyrun, and a successful
control against the same
account source; an ending must not imply that all work was lost when an
independent publication succeeded.
