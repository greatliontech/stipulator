# The freshness witness reds only inside a completed gate run

Lands: when gofresh's closure-analysis cost/memory rework lands (its
docs/issues/closure-analysis-cost-amortization.md) — rerun the gate and
read the surfaced failure.

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

## Resolution

After the gofresh rework shrinks the engine phase, run the gate on an
otherwise quiet machine and read the surfaced witness output. If it names
a degrade, the cause was environmental and dies with the rework; a real
assertion gets a fresh diagnosis.
