# Witness execution ignores the corpus test policy

Lands: when the witness/check execution contract next changes, or before gating a
corpus whose accepted test policy excludes a universal race run.

## Context

Witness verification invokes `go test -json -race ./...` as one universal policy.
It cannot express that a corpus races selected packages while running an
analysis-heavy package without race instrumentation, nor can it consume outcomes
from commands that already applied that policy. In Gofresh, the ordinary full suite
requires about eight minutes because root and closure tests repeatedly construct Go
package, SSA, and RTA fixtures; racing every package exceeds the default ten-minute
test timeout. Raising the timeout only makes the command eventually complete and
does not make the execution contract appropriate.

This is distinct from `check-discards-race-suite.md`: that issue concerns throwing
away a preceding complete suite. Even with one execution path, Stipulator still needs
to know which package commands constitute the corpus's accepted test policy. Running
only named bound tests is not automatically equivalent: package build failures,
`init`, `TestMain`, executable examples, fuzz seed replay, and packages without bound
tests remain part of a complete test policy.

## Resolution

Make witness execution consume an explicit, reviewable corpus test policy rather
than imposing `-race ./...`. The policy must identify every command and its package
scope, preserve complete command exit/build outcomes, and correlate named witness
outcomes with the exact source, toolchain, selection, and runtime-input evidence of
that command. Reuse a fresh prior outcome handoff or execute the policy once; never
run an independent universal suite afterward. Any package, example, fuzz seed, or
workspace member omitted by a bounded policy must be reported rather than silently
dropped.
