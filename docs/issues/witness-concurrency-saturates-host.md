# Witness concurrency saturates the host — a full corpus check can freeze an interactive machine

## Observed

A `stipulator check` over the gofresh corpus (~70 packages, race
instrumentation) made the author's workstation freeze intermittently
even though the process ran under `nice`. The run itself completes;
the machine is unusable while it does.

## Mechanism

`runSelectedPackages` fans packages out under
`bound := runtime.GOMAXPROCS(0)`
(internal/backends/golang/execute.go) — one `go test` process per
package, GOMAXPROCS of them at once. Each of those spawns compile
workers and a race-instrumented test binary whose own runtime uses
GOMAXPROCS threads, so the effective parallelism is a multiple of the
core count, and the race-build memory footprint multiplies per
concurrent package. `nice` lowers CPU priority only: memory pressure
and IO contention — the actual freeze mechanisms — are unmitigated.

## Shape of the fix (for disposition)

- A reviewed concurrency knob on the policy invocation (the envelope
  already carries reviewed execution attributes), defaulting to
  something host-friendlier than GOMAXPROCS for multi-process
  fan-out — e.g. `max(1, GOMAXPROCS/2)` — since each unit is itself a
  parallel process tree.
- Optionally: load- or memory-aware admission (hold new package spawns
  while the host is above a pressure threshold), and ionice/oom-score
  hints on spawned children.
- The same bound governs the non-race isolation pass and drift
  retries; one knob should cover all spawn fan-outs.

Lands: at the stipulator issue-elimination phase, or when the witness
executor's spawn path next changes, whichever comes first.
