# A freshness witness runs red only inside legacy whole-suite runs

Lands: when standalone `verify` and `gate` derive suite health and witness
evidence from the accepted policy's single execution or are retired (the
condition the `core-one-execution` gap record tracks) — close then; a
recurrence before that gets a fresh diagnosis.

## Observed

`TestRunTestsFresh` historically failed red only within completed
whole-corpus runs of the legacy witness runner and never reproduced in
isolation — not standalone, not under the race detector, not repeated. The
standing hypothesis is environmental degradation under whole-suite load:
the engine phase peaks around twelve gigabytes of resident memory and
historical failures coincided with out-of-memory kills.

Under the accepted test policy the red does not reproduce: the test passed
inside two green whole-corpus `stipulator check` runs (the unified executor
runs one owned process per package) and in a 161-second isolated run under
concurrent suite load. The exposure is therefore scoped to the legacy
runner (`RunTestsFreshContext`), which standalone `verify` and `gate`
still invoke; the canonical CI path no longer exercises it.

## Diagnostic and disposition

On a recurrence under the legacy path: isolate the freshness fixture, run
the failing operation, and read the surfaced witness stderr. If it names a
degrade, the cause was environmental (`TestRun.Degraded` carries the
phase); a real assertion gets a fresh diagnosis.
