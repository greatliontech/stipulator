# The check task discards complete race-suite outcomes

Lands: when the check/gate execution contract next changes.

## Context

`task check` first runs `go test -race` over every Stipulator package, then
invokes `stipulator gate`. The gate must derive witness evidence in its own run
and cannot consume the preceding command's outcomes, so it starts another
freshness analysis and then independently runs any stale or unproven tests. A
passing full suite therefore does not reduce gate analysis and may not reduce
test execution; its outcomes are discarded rather than reused as evidence.
Witnesses backed by source purity assertions or completed, compatible
observation-completeness proofs publish and serve across gate runs, so the
discarded work shrinks to whatever the edit actually staled — the double
execution remains, but only over the stale set rather than the whole suite.

Removing either command without defining their combined coverage can silently
drop checks: the ordinary suite may execute tests or examples the backend does
not enumerate, while only the gate correlates current-run outcomes with bindings.
The workflow needs one explicit execution contract, not an assumed subsumption.

## Resolution

Choose and enforce one witness execution path for `check`. Removing the
preceding suite requires the gate to be behaviorally equivalent to `go test
-race github.com/greatliontech/stipulator/...`: preserve package build and exit
failures, `init` and `TestMain` failures, executable examples, fuzz-seed replay,
packages without named runnable tests, and every workspace member. A
machine-readable outcome handoff instead must preserve the suite exit and bind
its outcomes to the current tree, toolchain, race selection, and runtime inputs
before the gate consumes them.
