# Protobuf backend

The protobuf backend verifies wire contracts at the layer where they live:
the descriptor, not generated code. Field numbers, reserved ranges, and enum
closedness are invisible or mangled in generated Go; binding wire clauses to
descriptors keeps evidence on the observable surface.

## Compilation

**REQ-proto-inprocess** (behavior): Descriptors MUST be produced in-process
via `github.com/bufbuild/protocompile` with source location information
retained; invoking an external protoc or buf toolchain is a defect, since
verification cannot depend on toolchain versions on PATH.

## Symbols and shapes

**REQ-proto-symbol** (behavior): A proto symbol reference MUST name the fully
qualified element name, the element kind, and, for fields, the field number.

**REQ-proto-shape-hash** (wire): The proto shape hash MUST be computed over a
canonical descriptor form that excludes source info and any ordering that is
not wire-significant, per REQ-model-hash-canonical-form; serialized
descriptor bytes are not a canonical form.

## Provers

**REQ-proto-provers** (behavior): The proto backend MUST provide descriptor
assertions covering element existence, field type and number, reserved names
and ranges, and closed-enum checks, as the initial prover set for `wire`
clauses.

## Claim sources

**REQ-proto-no-option-linkage** (behavior): The proto backend MUST NOT derive
binding claims from protobuf options; an option is not executed and is
therefore an unverifiable in-artifact claim, and the binding store remains
the only claim source.
