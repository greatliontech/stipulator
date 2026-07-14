# The runtime-input digest hashes fixture content after the run

Lands: when gofresh grows pre-run manifest evaluation, or when witness
records are next redesigned.

## Context

Witness closure fingerprints are captured before the selective run, so a
source edit made while tests execute records a hash the edited tree no
longer matches — Stale, the safe direction. The runtime-input manifest has
no equivalent hoist: the testlog only names the paths a run read, and
`runtimeinput.FromTestLog` hashes their content when it parses the log,
after the run. A fixture edited while the tests that read it execute is
digested at its post-edit content while the outcomes rode the pre-edit
content; the next check finds the digest current and can serve those
outcomes. The window is one invocation's span, requires an edit to a file
the running tests read, and closes at the next edit of that fixture.

Hashing before the run is structurally unavailable — the path set is only
known from the testlog the run produces.

The closure half of the same family: fingerprints are captured before the
run, so a single mid-run edit records a hash the edited tree no longer
matches — stale, safe. An edit and its exact revert both landing inside one
package's capture-compile-run span records a hash the reverted tree matches
over outcomes a transiently-edited binary produced. Residual to any
capture-once scheme; the same pre-run-evaluation support would close both
halves.

## Resolution

Pre-run manifest evaluation needs the path set ahead of execution: either
gofresh-side support (a recorded manifest re-digested at invocation start
for previously-cached tests) or a record redesign that stores content
observed by the run itself.

Widened by parallel witness execution: with packages running
concurrently, a sibling witness that mutates another package's
observed input inside its read-to-hash window creates the same wrong
pin without any human edit — the first self-generated instance of
this window. The harness assumes package-parallel-safe witnesses (the
`go test` baseline, declared in evidence.md); this issue remains the
tracking point for closing the window mechanically (pre-run manifest
evaluation in gofresh, or post-run interference detection).
