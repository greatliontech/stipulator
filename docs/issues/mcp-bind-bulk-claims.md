# MCP bind writes only one binding claim per call

Lands: when the MCP `bind` input schema or binding record verb next changes.

## Observed

Migrating a consuming repository from prose specs to Stipulator required many
validated binding claims: behavior requirements needed test witnesses, and the
gomutant handoff also needed implementation bindings. The MCP `bind` tool
accepted exactly one requirement, symbol, and role per call, so authoring the
initial binding set required dozens of round trips.

This is distinct from the existing gap-bulk issue: the bind operation is also a
record verb with validation, pinning, and machine-owned writes. The repeated MCP
calls were safe, but the ergonomics made large initial migrations slow and made
it harder to review the intended binding batch as one operation.

## Resolution

Add a batch binding surface that validates an array of binding claims and writes
them atomically, or returns per-claim diagnostics without committing partial
state. Each claim should carry the same fields as the single-claim tool:
requirement, backend, symbol, role, and optional target binding file.

Atomic batch semantics would let agents migrate a corpus from a reviewed mapping
while preserving the same validation guarantees as the single-claim verb.
