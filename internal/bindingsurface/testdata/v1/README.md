# Binding-surface v1 fixtures

These are the authoritative examples for
`stipulator.binding-surfaces/v1`, whose contract is
[`docs/specs/binding-surfaces.md`](../../../../docs/specs/binding-surfaces.md).
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

- `bad-id`, `unknown-format`, `unknown-role`: invalid scalar semantics.
- `noncanonical-*`: valid identifiers over values placed in noncanonical order.
- `duplicate-*`: repeated requirements, bindings, surfaces, identifiers, and
  surface keys forbidden by the surface contract.
- `unknown-field`, `duplicate-field`, `trailing-json`, `invalid-surrogate`:
  malformed ProtoJSON inputs.

Consumer-policy fixtures:

- `gomutant-invalid/null-surfaces.json` is accepted as unset by permissive
  ProtoJSON decoding but rejected by gomutant's stricter adapter contract.
- `gomutant-invalid/invalid-requirement.json` and `empty-*-symbol.json` carry
  internally consistent identifiers over values Stipulator never emits and
  gomutant rejects before target resolution.
- `valid/mixed-backend.json` is producer-valid but rejected by gomutant's
  Go-only adapter rather than silently narrowed.

`TestContractFixtures` derives every producer-valid report from binding claims
and derives each canonical-invalid report by one named mutation of valid
content. Consumers copy these JSON files and validate them through their own
adapter without importing Stipulator's implementation.
