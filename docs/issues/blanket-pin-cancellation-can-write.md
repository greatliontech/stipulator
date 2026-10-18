# Blanket pin can write and report completion after cancellation

Lands: tooling-redesign 5.2e

This adjacent finding remains with the tooling agent for repair/integration. The
user explicitly requested that broader residue be filed rather than expanding the
Cerebro explicit-bind/named-pin repair or weakening acceptance. Blanket pin is
outside that repair; its behavior is not an accepted cancellation exception.

## Reachable path

Source inspected at base `523529e` and the uncommitted explicit-authoring repair:

1. Blanket CLI `internal/cmd/pin.go` and MCP `internal/mcpserver/pin.go` call
   `author.ResolveShapes` with no requirement filter.
2. `internal/author/pinshapes.go` reports a resolver error through its callback,
   skips that symbol and returns the shapes accumulated so far. Cancellation
   during a typed resolver query can take this error path.
3. Neither blanket caller checks the operation context after resolution. Each
   proceeds to `records.Pin` and applies updates. An unset content pin can be
   backfilled even though no shape answer was obtained for its binding.
4. CLI can return success; MCP can publish a COMPLETED terminal after its writes.

The proximate cause is treating cancellation like a recoverable per-symbol shape
diagnostic, without a terminal context check before the later write/completion.
This is source-established reachability, not a dedicated executed reproduction or
a claim that every cancellation writes. Existing mutation tests of the repaired
named arm do not verify the blanket arm.

## Repair and acceptance

Add controlled CLI and MCP regressions that cancel during actual blanket-arm shape
resolution with an unset consent field capable of producing a write. Assert the
correct terminal/error and actual record state. Separate cancellation before
writes from interruption after a write has landed; report any landed paths
truthfully. Preserve ordinary non-cancellation shape-fault diagnostics, blanket
backfill/non-laundering rules and named-pin behavior.

Review the minimal blanket-arm cancellation disposition against the existing
interruption and write-accounting contracts before changing sequencing. Do not
make `ResolveShapes` globally reject all per-symbol errors merely to fix this
case. Independently verify a deliberate cancellation-check removal is detected.
This issue grants no full-framework or current blanket-cancellation acceptance.
