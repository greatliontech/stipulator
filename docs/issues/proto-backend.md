# Conditional Protobuf backend

Lands: tooling-redesign 9.4

Activation condition: a party outside this repository consumes Stipulator's
protos — a module importing `gen/stipulator/v1`, or a non-Go consumer of
`proto/stipulator/v1` — requiring wire evidence that existing Go witnesses
and shape pins cannot supply. The census owns preserving and checking this
condition; its slot does not commission backend implementation immediately.

`docs/specs/backends/proto.md` specifies in-process descriptor analysis,
symbols, canonical descriptor hashes, provers and no option-derived claims.
No proto backend is registered. The twelve unverifiable `backend: "proto"`
claims were removed in the 2026-10-06 records-gate work. Five REQ-proto-*
gaps and the two root gaps on REQ-core-proto-io and REQ-model-graph now
share the external-consumer condition, rather than depending on an
unimplemented prover or an aspiration that work be scheduled.

Retain these design inputs when the condition fires:

- Resolve workspace imports and well-known types from committed configuration.
  An external-dependency strategy must not silently depend on whatever happens
  to populate Buf's cache; vendored exports or an explicit lock/cache contract
  need their own determinism argument.
- Separate canonical descriptor evidence from behavioral Go witnesses and
  analyzer proofs. Parameterized assertions need a committed declaration
  home; reserved-range monotonicity is a change judgment, not a timeless proof.
- Reconcile the complete REQ-proto-provers contract before choosing a subset;
  partial implementation cannot silently shrink the specification.
- Backend-qualify symbol keys in pin/shape-mismatch reports. The 2026-08-29
  pin-reporting observation identified bare-symbol keys in `internal/records`
  (`Pin`, `ShapeMismatched`); another backend can name the same string.

The remaining obligation is conditional descriptor-level verification and
its bindings, not resurrecting unverifiable claims. Retirement would be a
separate scope decision through the normal disposition path, not a consequence
of having no external consumer yet.
