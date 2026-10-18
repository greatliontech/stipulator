# Explicit bind and named pin resolve the whole workspace

Lands: tooling-redesign 5.2e

Triage the authoring load boundary separately from the existing scoped-check cost
issue. The user requested this filing on 2026-10-18 and will decide with the tool
agent whether to deliver the repair upstream or use a local repair in the consumer
workflow. This filing authorizes neither implementation nor a tool upgrade.

## Confirmed source path

Inspection covered installed v0.74.5 source and the clean local checkout at
`9c650932d5a2782e78a3835bb99ed52a36e758ff` (`v0.74.5-3-g9c65093`).
The consumer's selected binary is `/home/nikolas/go/bin/stipulator`, v0.74.5,
SHA-256 `32676198185e4572add5a6d27b39fb6f31c1083b91d397f13be785d6156a252b`.

- CLI `internal/cmd/bind.go` parses an explicit claim batch but obtains the
  whole-tree backend through `makeBackends` in `internal/cmd/root.go`.
- CLI `internal/cmd/pin.go` restricts selected requirement records, then resolves
  their shapes through that backend before writing editorial consent.
- `internal/backends/golang/served.go`: `NewWholeTree` is lazy. Its construction
  does not load packages. The first resolution starts the child; whole-tree mode
  leaves `childPatterns()` nil rather than supplying selected package patterns.
- `internal/backends/golang/resolverclient.go` and `golang.go` distinguish nil
  patterns (whole tree, `./...` per selected member) from explicit package patterns.
  The internal package-scoped resolver already exists; these authoring entry
  points do not connect their explicit symbol sets to it.
- MCP `internal/mcpserver/pin.go` writes editorial updates before resolving shapes
  through its whole-tree backend. Cancellation after those writes is not a bounded
  authoring remedy. CLI and MCP sequencing must be considered independently.

The reachable path is an explicit bind or named pin followed by its first shape
query: a small claim set still initiates whole-workspace resolution. This is a
load-scope finding, not proof that current symbol answers are incorrect.

## Consumer impact and evidence limits

Cerebro has 58 text-stale claims across seven changed requirements and two separate
pre-existing stale claims. It also needs deliberate new T1 temporal-condition and
generation witnesses, plus justified current-shape reconciliation for changed
witnesses. External tests and review do not replace native authoring. Protected
user bindings, native shapes and existing record semantics must remain intact.

No current v0.74.5 authoring timeout was reproduced for this filing. No discovery
retry, binding write, binary switch or workaround was attempted. The recorded
600-second timeout belongs to an older c9ea6cd **check**, tracked in
[cerebro-scoped-discovery-cost](cerebro-scoped-discovery-cost.md); it is not a
measurement of this version's bind/pin latency. The separate
[MCP export compatibility issue](mcp-go-export-compatibility-authoring.md) is not
evidence of this failure mode either.

Retained source trace, binary metadata and consumer operation inventory are at
`~/.cache/cerebro-tmp/consolidation-20261018/native-authoring-route-audit.md` on the
reporting host. The conclusion above is supported by source inspection; current
cost attribution and measured bounded completion remain to be established.

## Candidate repair and acceptance

Evaluate feeding the complete explicit bind batch or selected pin claim symbol
set into the existing package-scoped resolver before its first load. Preserve
workspace/member ownership, every policy build selection, toolchain provenance,
test variants, promoted declarations, generated-file checks, native shape
computation, clause validation and write behavior. Dependencies required to answer
the selected claims must still resolve; package scoping is not permission to omit
them. Do not broaden this into full-framework restoration or a new resolver design.

Acceptance needs tests demonstrating selected-root loading, answer/refusal parity,
multi-package batches and policy selections, relevant CLI/MCP behavior, and honest
failure/cancellation writes. Qualify the delivered binary with representative
consumer authoring under recorded inputs and selection before changing consent.
Text-only consent without shape judgment would be a separate public-contract
decision and would not by itself unblock new bindings or shape consent.

Do not fabricate hashes, use meaning-preserving editorial disposal for semantic
changes, blanket-repin records, or present bounded authoring as a healthy full
framework verdict. Consumer enforcement reconciliation remains Cerebro plan 14.3;
the tool-side load boundary is routed here for triage and delivery decision.
