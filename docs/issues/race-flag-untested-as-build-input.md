# The -race build input has no stipulator-side witness

Lands: when witnessing grows a second rigor mode (a no-race or coverage
variant) or the build-input surface next changes.

## Context

RunTestsFresh pins `-race` into every witness fingerprint as a
caller-supplied build input (gofresh `WithBuildInputs`), and runs the
selective executions with the same flag. The clause
(REQ-evidence-witness-freshness) names the flag as part of the fingerprint;
gofresh's own suite witnesses that build inputs fold into the BuildConfig
guard, but no stipulator test varies the flag and observes a verdict flip —
dropping the `WithBuildInputs("-race")` argument survives the current suite
because both runs of the e2e use the same flag.

## Resolution

A witness that captures under one build-input set and checks under another,
asserting Stale — cheap once the engine surface is exercised with two
configurations in one test.
