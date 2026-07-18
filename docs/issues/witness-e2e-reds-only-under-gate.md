# A freshness witness runs red only inside completed gate runs

Lands: when witness execution can apply the accepted test policy and the
isolated diagnostic below has been executed and dispositioned.

## Observed

`TestRunTestsFresh` has failed red only within completed whole-corpus gate
runs and has never reproduced in isolation — not standalone, not under the
race detector, not repeated. The standing hypothesis is environmental
degradation under whole-suite load: the engine phase peaks around twelve
gigabytes of resident memory and historical failures coincided with
out-of-memory kills. Per-phase degradation instrumentation already exists on
the test's run record (`TestRun.Degraded`), so a surfaced failure names the
phase that degraded.

## Diagnostic and disposition

Isolate the freshness fixture, run the gate, and read the surfaced witness
stderr. If it names a degrade, the cause was environmental; a real assertion
gets a fresh diagnosis.
