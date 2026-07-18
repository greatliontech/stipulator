# The runtime-input digest hashes fixture content after the run

Lands: when the executor captures per-package observation brackets
pre-spawn — which requires package-directory resolution to move ahead of
process spawn — and publishGroup seals completed observations on them.

## Context

Gofresh now carries the closing mechanism (value binding landed):
constructing a completed observation requires an observation bracket
(`runtimeinput.CaptureBracket` supplied through `runtimeinput.WithBracket`,
spec REQ-inputs-value-binding) — a fingerprint over caller-declared
candidate roots captured before the producing process starts and
revalidated strictly after the manifest digest's last input read. A
fixture mutated inside the run-to-ingest window moves the bracket and the
observation seals unverifiable, toward recomputation; an observed identity
resolving under no declared root seals per-identity unverifiable. What
remains is this consumer's adoption: the executor capturing per-package
brackets pre-spawn and publishGroup sealing completed observations on
them.

Migration note: reads resolving outside declared roots become permanently
per-identity unverifiable — the consumer's root policy owns the
recommended default. Blast radius: only stipulator's
observe.go/freshness.go/stream fuzzer construct completed observations;
pew uses IncompleteEnv and is unaffected.

The original window, for the record:

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

The gofresh-side support exists: the observation bracket sidesteps the
path-set-only-known-post-run limit by fingerprinting caller-declared
roots pre-spawn, so the remaining work is entirely on this side —
resolve each package directory before its process spawns, capture a
bracket over the declared roots there, and pass it to the observation
construction publishGroup seals on.

Widened by parallel witness execution: with packages running
concurrently, a sibling witness that mutates another package's
observed input inside its read-to-hash window creates the same wrong
pin without any human edit — the first self-generated instance of
this window. The harness assumes package-parallel-safe witnesses (the
`go test` baseline, declared in evidence.md); this issue remains the
tracking point for closing the window mechanically — per-package brackets
captured pre-spawn, per the landing condition.

The policy executor's per-process observations share the identical
window: each launched process's testlog is ingested after the process
exits, digesting observed content at post-run state while the outcomes
rode what the run read, and packages fan out concurrently within an
invocation exactly like parallel witnesses. The same landing condition
covers both capture sites.
