# Explicit partial acceptance for an implemented capability

Lands: Gofresh docs/plans/tooling-redesign.md 7.5 for the capability's derivation,
with its conditional disposition retained through 9.4. This filing does not
authorize implementation of a new partial-acceptance mode.

## Consumer need

git-go has a compiled design corpus and is implementing complete public
capabilities incrementally. Its accepted roadmap requires an executable gate
for the implemented requirement set, whole-module tests for unbound tests, and
an honest account of the remaining unimplemented design. The complete corpus
must remain present and visibly uncovered; a partial acceptance result must
not be represented as whole-library conformance.

The first value capability names eleven requirements explicitly. The consumer
needs a native, failure-sensitive acceptance judgment for that set without
declaring thousands of unrelated implementation gaps merely to make the global
verdict pass, and without constructing its own replacement verdict from JSON.

## Observed installed behavior

On git-go's initial object-value implementation, these direct checks passed:
Go build/vet, ordinary whole-module race tests, and the separately selected
native Git ID-encoding suite for both formats. The installed Stipulator is
clean revision `372ef9933bb564e71b4fe62ed74df22173ac5757`, using Gofresh
`v0.115.1`, built with `go1.27.2-dst.15`.

With `GITGO_GIT` naming the explicit conforming oracle, run in git-go:

```sh
mlock run stipulator gate --req REQ-oid-formats,REQ-oid-value,REQ-oid-equality,REQ-oid-absent-unique,REQ-oid-zero-value,REQ-oid-parse,REQ-oid-decode,REQ-oid-invalid,REQ-oid-write,REQ-oid-write-mismatch,REQ-oid-order
```

The report states `11 covered, 0 uncovered, 0 partial, 0 stale, 0 broken`,
then lists the remaining corpus's uncovered requirements as violations and
ends `gate: fail`. A `check --ids REQ-oid-parse` likewise executes the two
bound passing witnesses, identifies its scope as partial, and remains red
for unrelated uncovered requirements. This is a workflow limitation, not a
claim that these behaviors violate the current whole-corpus gate contract:
`REQ-gate-no-undeclared` requires declarations for pre-existing reds, and
`REQ-check-verdict-scoped` excludes reds caused solely by the execution
boundary, not requirements already uncovered independently of that boundary.

## Derivation and acceptance questions

Derive whether and how the native tool should express explicit partial
capability acceptance separately from execution scope and report filtering.
Preserve the global verdict and the current red corpus; a caller choosing a
closed requirement set does not establish coverage outside it.

The selected judgment needs complete requested membership, current consent
and resolvable bindings, the evidence class required by each clause, and
failure propagation from the deciding executions. Unknown identifiers,
missing selected rows, unsupported evidence and observed failures cannot
produce a vacuous pass. State how record hygiene, shared witnesses, executed
unbound tests and suite health participate rather than silently assuming
that a filtered coverage count is the complete judgment.

The requesting user selected filing this workflow request and leaving
git-go's executable acceptance gate pending. No Stipulator implementation
change, blanket migration-gap declaration or local verdict wrapper is
requested by this filing. Re-evaluate against the current installed consumer
after the tooling migration; the observation above identifies one build,
not a claim about every newer implementation.
