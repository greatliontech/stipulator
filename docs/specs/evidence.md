# Bindings, evidence, coverage

The claim/evidence split is the trust boundary of the system: bindings are
committed, reviewable claims authored by humans or agents; evidence exists
only as the output of stipulator verifying those claims against the current
corpus and code. Nothing an agent writes can make a requirement covered.

## Bindings

**REQ-evidence-binding-store** (behavior): Binding claims MUST be stored as
textproto files under `.stipulator/bindings/`, each naming a requirement
identifier, the content hash it was authored against (unset when not yet
pinned), a backend, a symbol reference, a role, and — when the backend
defines one — the shape hash of the bound symbol.

**REQ-evidence-binding-machine-owned** (behavior): A tool rewrite of a
binding file MUST fail when the file carries comments outside its leading
header block, so hand-written commentary is never silently destroyed;
binding rationale belongs in review and commit messages, not in record
files.

**REQ-pin-backfill** (behavior): The pin operation MUST set only unset
content pins and shape pins — a differing content pin is never rewritten
outside an editorial disposition, so staleness cannot be laundered by a
blanket re-pin.

**REQ-evidence-record-verbs** (behavior): Binding records MUST be writable
through tool operations that validate at write time — the requirement
against the compiled corpus, the symbol through its backend when one exists
— rendering through the machine-owned writer with the content pin, and the
shape pin when the backend has a verifier, applied immediately, so a freshly
authored claim is never born stale.

**REQ-evidence-binding-roles** (behavior): A binding's role MUST be one of
`implements` (the symbol realizes the requirement), `tests` (the symbol is a
test exercising it), or `proves` (a backend prover assertion checks it).

**REQ-evidence-generated-code** (behavior): A binding claim targeting a
generated source file MUST be rejected, with guidance to bind the generating
artifact instead.

## Evidence

**REQ-evidence-promotion** (invariant): Evidence MUST exist only as the
result of verifying a binding against the current corpus and code in the
current run; see REQ-core-claims-untrusted.

**REQ-evidence-ladder** (behavior): Evidence strength MUST be ordered,
strongest first:

1. analyzer proof
2. property witness (a generator-driven test quantifying over inputs)
3. example witness (a test exercising named cases)
4. static binding (the symbol resolves and its shape hash matches)
5. attestation

**REQ-evidence-witness** (behavior): A witness MUST record that a named test
passed in the current verification run while bound — or registered at
runtime — to the requirement.

**REQ-evidence-run-attributes** (behavior): A witness MUST carry the rigor
attributes of the run that produced it, at minimum whether the race
detector was enabled.

**REQ-evidence-attestation** (behavior): An attestation MUST carry its reason
text and appear distinctly in every coverage output; it is the weakest
evidence and is never silently aggregated into stronger kinds.

## Coverage

**REQ-coverage-policy** (behavior): Coverage MUST be evaluated per
requirement by a policy mapping the pair (clause kind, normative keyword) to
a minimum required evidence, with the manifest able to override the defaults.

**REQ-coverage-policy-default** (behavior, refines REQ-coverage-policy): The
default policy MUST require, for `MUST`/`MUST NOT` requirements, the minimum
evidence listed per clause kind below; for `SHOULD`/`SHOULD NOT`, a static
binding or an attestation; and for `MAY`, a static binding when bound, with
unbound `MAY` requirements exempt from coverage.

| Clause kind | Minimum evidence |
|---|---|
| `behavior` | executed witness (property or example) |
| `invariant` | property witness or analyzer proof |
| `structural` | analyzer proof |
| `wire` | analyzer proof or executed witness |

**REQ-coverage-buckets** (behavior): Each non-exempt requirement MUST be
reported in exactly one bucket — any broken binding forces `broken`, else
any stale binding forces `stale`, then the policy decides `covered` against
`uncovered`; claim hygiene is part of coverage, so red claims downgrade a
requirement even when other evidence satisfies the policy:

| Bucket | Meaning |
|---|---|
| `covered` | policy met by current evidence |
| `broken` | a binding fails to resolve, its shape hash mismatches, or its bound test fails or produces no outcome in a witnessed run |
| `stale` | a binding whose content-hash pin is unset or differs from the current one |
| `uncovered` | no evidence meets policy |

**REQ-coverage-no-scalar** (behavior): Stipulator MUST NOT gate on aggregate
percentages; gating is expressed only over requirement sets and buckets.

## Backend interface

**REQ-backend-surfaces** (structural): A backend MUST consist of exactly
three surfaces: a symbol reference scheme, a shape-hash definition, and a set
of provers.

**REQ-backend-core-neutral** (structural): The compilation, binding,
coverage, and change models MUST NOT depend on any backend; backend-specific
knowledge is confined to symbol reference interpretation and proving.
