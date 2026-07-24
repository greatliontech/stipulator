# check serves and executes zero witnesses, failing every behavior binding as unwitnessed

## Observed

On a warm tree (build green, full `go test ./...` green), both the CLI `stipulator check`
and the MCP `check` tool report every behavior-bound requirement red with
`bound test <pkg>.<Test> produced no outcome (unwitnessed)` while the result carries
`testsExecuted: 0, testsServed: 0, testsUncacheable: 0`. The default mode's contract
("serves fresh witness evidence and executes only the stale remainder") implies the stale
remainder — here, everything — should have been executed; instead no execution is attempted
and no diagnostic explains why. A follow-up CLI run (no timeout, exit 0) reproduces the same
state, so it is stable, not a transient cancellation.

Environment: cerebro repo, linux/arch, Go toolchain current, observed 2026-07-24. The same
machine deterministically panics gofresh v0.34.0's RTA analysis under gomutant
(`closure/rta.go:389 addRuntimeType: panic: K`), so a shared analysis-layer fault on this
machine is plausible.

## Why it matters

`check` degrades to "fail everything" with no signal distinguishing "your tree is wrong"
from "the witness engine never ran". A caller scripting on the verdict cannot tell the
difference; the reason string blames each bound test rather than the execution layer.

## Wanted

- When the witness engine executes nothing while stale bindings exist, say so once at the
  result level (an execution-layer error), instead of emitting per-binding `unwitnessed`
  reds that read as tree defects.
- Surface the underlying execution error (spawn failure, analysis panic, policy mismatch)
  in the check result.
