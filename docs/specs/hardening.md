# Witness hardening

A passing test proves execution, not sensitivity: a witness is worth its
tier only if breaking the implementation breaks the test. stipulator makes
that mechanical at its seams and delegates the breaking itself: vacuity is
rejected at binding write time, the mutation surface exports as a versioned
document a mutation engine consumes, and the engine's findings read back by
label — the mutation contract (operators, execution, records, attestation)
lives in the engine's own specification. Survivors are findings for
judgment; the machine proves a test can fail, never that it fails for the
reason the spec means.

**body hash** (term): a backend-defined hash of a symbol's implementation
body; the version of behavior a hardening record vouches against — the
sibling of the shape hash, which covers only the declaration.

**REQ-harden-vacuity** (behavior): A `tests`- or `proves`-role binding
whose bound test contains no failure path — no failing testing call, no
delegation to a callee that receives a testing handle, and no panic —
MUST be rejected at write time, resolved from the code; reachability is
deliberately not decided here, that is what mutation is for.

**REQ-harden-export** (behavior): The `targets` operation MUST export the
mutation surface as stipulator's own versioned document —
`{"stipulatorTargets": 1, "targets": [{"symbol", "witnesses",
"requirements"}]}` — one entry per symbol bound `implements` by an
in-corpus requirement (a dangling binding is verify's diagnostic, never
the export's silent narrowing), carrying the union of the witness-role
bound tests of every such requirement and those requirement identifiers as
opaque labels, deterministically ordered so an export commits and diffs
stably. A witness-less entry exports with an empty witness set and means
exactly that — nothing vouches; the export is a complete statement, and an
engine must not substitute tests stipulator never bound. The format is
stipulator's contract with any mutation engine that consumes it: the engine
parses this document and returns its findings in a document of its own that
stipulator reads back by label, so the two tools compose through documents
— never a shared library, never an invocation.

**REQ-harden-staged-scope** (behavior): The `targets` operation MUST offer a
staged-delta classification, scoping the change set to the working tree
against `HEAD`, that reports for each changed implementation symbol — a
symbol whose body differs from its `HEAD` form, in a non-test source file,
since test sources are witnesses rather than mutation targets — whether
hardening covers it or the specific reason it does not: covered, a bound
`implements` symbol with a resolving witness whose class the body mutator can
break; no bound implementation; no resolving witness; a witness class outside
the body-mutation operator set; a generated or data-only file; or an
integration seam, a changed file that declares no such body. The
classification is exploration, never a gate (REQ-harden-exploration): its sole
purpose is to make the manual-mutation tail explicit, so the operator hardens
the covered surface and hand-mutates only what the report marks unreachable.

**REQ-harden-findings** (behavior): stipulator MUST read the mutation
engine's findings document — the versioned record the engine maintains at
the tree root, refused at an unknown version, unknown fields ignored, a
missing document reading as nothing measured — and recover requirement
scoping from the labels it exported, never from the engine's internals: the
document is the engine's contract, the reading is advisory (its surfaces
never gate), and the pins stipulator judges are the ones it can compute
itself — body and witness hashes (canon is hash-compatible by design) and
the toolchain — while operator-set drift is the engine's own re-measure
concern, invisible and unjudged here.

**REQ-harden-coverage-reminder** (behavior): The gate MUST report, without
affecting its verdict (REQ-harden-exploration), each covered requirement
whose implementation body has no fresh finding — a function bound
`implements` that no recorded finding covers, or whose finding no longer
matches the current body hash, witness set and content, or toolchain
(REQ-harden-findings) — distinguishing a body a mutator can harden
(witnessed by a test the mutator breaks) from one with no mutation target
(witnessed only outside the operator set, which the staged-delta report
explains). A non-function binding, having no body to mutate, is never
reminded, and a body whose finding is fresh drops off, so the reminder
shrinks to empty as coverage is hardened. The reminder makes the covered but
unhardened tail explicit; it is never a gate input.

**REQ-harden-exploration** (behavior): Hardening campaigns MUST NOT feed
the gate — survivors are findings awaiting disposition, a strengthened
test or an attested equivalence, never automatic failures.
