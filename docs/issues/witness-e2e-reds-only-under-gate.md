# The freshness witness reds only inside a completed gate run

Lands: when witness execution can apply the corpus test policy without an independent
universal race run — rerun the isolated gate diagnostic and read the surfaced
failure.

## Context

TestRunTestsFresh passes every reproduction outside the gate: bare (166s),
under -race alone (511s), and under -race with the gate's exact
invocation — same pattern of all fifteen package tests, GOWORK=off, outer
testlogfile (719s). Inside the two gate runs that completed it failed, and
the failure text was unrecoverable at the time (the selective runner
discarded test output; failed-witness output surfacing landed after the
last completed gate). Two later gate attempts never produced a verdict:
the gate binary was SIGKILLed mid-analysis — the engine phase holds ~12 GB
for ~127 subjects and dies under ordinary desktop memory load.

The leading suspicion is the same memory pressure: the resident engine
starving the witness's inner race-instrumented builds into a degraded run,
which the e2e now fails loudly with the fault named (TestRun.Degraded,
asserted per phase). That instrumentation and the failed-witness output
surfacing are already in place, so the next completed gate names the
failure on its own stderr.

After race-selected analysis landed, isolating the freshness fixture reduced
the complete outer `go test -race` phase to 313.410s. The immediately following
gate still did not reach a verdict before manual termination: it starts a new
whole-tree freshness analysis rather than consuming those test outcomes, and
would then run any stale or unproven tests independently. The discarded-outcome
workflow is tracked separately; even without the preceding test phase, the
gate's per-subject engine cost remained the blocker. Gofresh's maximal-default batch
rework removed that blocker, but a direct Gofresh verification then demonstrated the
separate universal-race policy cost tracked in
`witness-execution-ignores-test-policy.md`; the diagnostic remains parked until it can
run under the accepted corpus policy.

## Resolution

Run the isolated gate diagnostic under the accepted corpus policy and read the
surfaced witness output. If it names a degrade, the cause was environmental; a real
assertion gets a fresh diagnosis.
