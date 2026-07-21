# The partitions export form's uncapped call is unpinned at the tool seam

Lands: when an MCP fixture with a closure exceeding OverlapCap becomes
proportionate, when the partitions tool seam next changes, or — for the
prune residual below — when prune's CLI seam next changes.

The partitions tool's export form calls `ProtoUncapped()` — pinned at
the facts layer (the method itself is uncapped) — but a tool-seam swap
back to the capped `Proto()` is undetectable with small fixtures: a
fixture exceeding OverlapCap through the MCP harness needs a
12-component closure, disproportionate today.

Related residual of the same call-path-identity class: CLI prune's
serving-class refusal (`verify.ServingClassRequired` after
`witnessRun`) is enforced at runtime and unit-pinned on the shared
helper, but no CLI-level test executes prune's witnessed path, so
dropping that one call site would survive the suite; the MCP prune
seam is pinned (`TestPruneRefusesNonServingEvidence`), and any callee
swap still refuses loudly at runtime on both surfaces.
