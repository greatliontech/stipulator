# Long-running MCP gate and context calls time out opaquely

Lands: when MCP long-running operation semantics next change.

## Observed

`stipulator_gate` and `stipulator_context` hard-timeout after approximately 120 seconds with
only `MCP error -32001: Request timed out`. The equivalent `stipulator gate` CLI invocation
can continue beyond 120 seconds and reports witness progress, while the MCP caller receives no
result or phase information.

Both MCP tools synchronously run the witness pipeline but expose neither progress nor a result
that distinguishes a client deadline, server cancellation, test failure, and server failure. A
valid corpus whose witness run exceeds the harness deadline is therefore unusable through the
agent-facing surface without distinguishing long-running work from a server failure, while the
CLI exposes the current phase.

## Resolution

Define and validate long-running MCP behavior. Calls must either remain usable for the supported
gate duration or expose a resumable, progress-bearing operation. Deadline failures must identify
the elapsed phase and cause. Cover both `gate` and `context`, since context can also run long enough
to exceed the client deadline.
