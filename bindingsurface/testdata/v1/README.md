# Binding-surface v1 fixtures

These are the authoritative examples for
`stipulator.binding-surfaces/v1`, whose contract is
[`docs/specs/binding-surfaces.md`](../../../docs/specs/binding-surfaces.md).
JSON whitespace is not contractual; message values, repeated-field order,
duplicates, and identifiers are.

Producer-valid reports:

- `valid/full.json` contains a witness-less surface and a shared implementation
  with `tests` and `proves` bindings, including one executable symbol under both
  roles; one requirement is also implemented by two surfaces.
- `valid/empty.json` is the valid empty report.
- `valid/mixed-backend.json` contains both a Go implementation with a
  Proto-associated binding and a top-level Proto implementation surface.
- `valid/unicode.json` uses composed and decomposed non-ASCII symbols to pin
  UTF-8 byte lengths and the absence of Unicode normalization.

Contract-invalid reports:

- `bad-id`, `mismatched-id`, `unknown-format`, `unknown-role`: invalid scalar
  semantics.
- `noncanonical-*`: valid identifiers over values placed in noncanonical order.
- `duplicate-*`: repeated requirements, bindings, surfaces, and surface keys
  forbidden by the surface contract.
- `unknown-field`, `duplicate-field`, `trailing-json`, `invalid-surrogate`:
  malformed ProtoJSON inputs.

Consumer-edge fixtures:

- `gomutant-invalid/null-surfaces.json` is accepted as unset by permissive
  ProtoJSON decoding but rejected by this module's strict codec.
- `gomutant-invalid/invalid-requirement.json` and `empty-*-symbol.json` carry
  internally consistent identifiers over values Stipulator never emits; this
  module rejects them during canonical validation before consumer adaptation.
- `valid/mixed-backend.json` is producer-valid but rejected by gomutant's
  Go-only adapter rather than silently narrowed.

`TestContractFixtures` parses every producer-valid report and rejects every
invalid report through the public module API. Consumers copy these JSON files
to test their adaptation policy without importing Stipulator's implementation.
