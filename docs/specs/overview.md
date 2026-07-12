# Stipulator overview

Stipulator is a specification compiler and conformance verifier. It compiles
RFC-style markdown specifications into a protobuf intermediate representation,
tracks which requirements are satisfied by which code through verifiable
evidence, and reports behavior coverage deterministically. It is built to be
consumed by LLM harnesses: agents receive self-contained spec bundles, return
binding claims, and the tool — never the agent — decides what is proven.

## Pipeline

Non-normative orientation; each stage is specified in its own document.

1. **Author** — humans or LLMs write spec documents in the authoring profile ([profile.md](profile.md)).
2. **Compile** — the corpus compiles to the IR; lints reject profile violations ([model.md](model.md)).
3. **Chunk** — self-contained bundles fan out to agents ([model.md](model.md)).
4. **Bind** — agents submit binding claims ([evidence.md](evidence.md)).
5. **Verify** — language backends promote claims to evidence or reject them ([evidence.md](evidence.md), `backends/`).
6. **Report** — coverage buckets and gap evaluation gate the change ([change.md](change.md)).

## Terminology

**corpus** (term): the set of markdown documents enumerated by the manifest;
the unit of compilation.

**manifest** (term): the file `.stipulator/manifest.textproto`; declares the
corpus and configuration.

**IR** (term): the intermediate representation — the protobuf graph a corpus
compiles to.

**requirement** (term): a single normative statement with a permanent
identifier and a clause kind; the unit of identity, coverage, and chunking.

**clause kind** (term): the type of a requirement — `behavior`, `invariant`,
`structural`, or `wire` — determining what evidence can satisfy it.

**payload** (term): the contiguous list and table blocks following a
requirement's lead paragraph, forming part of its text.

**canonical form** (term): the explicitly specified normalization of an
object over which a hash is defined.

**content hash** (term): the hash of a requirement's or term's canonical
text; the version of an identity.

**shape hash** (term): a backend-defined hash of a bound symbol's declared
shape; the version of a binding target.

**binding** (term): a committed claim that a symbol implements, tests, or
proves a requirement, pinned to a content hash. A binding is an input to
verification, never a result of it.

**evidence** (term): a verified binding — produced only by stipulator
checking a claim against the current corpus and code.

Evidence is distinct from advisory attachments: out-of-band material may be
correlated and rendered, but only a backend Stipulator invokes in the current
verification run can produce evidence.

**witness** (term): evidence that a named test passed while bound to a
requirement in the same verification run.

**prover** (term): a backend component that verifies claims — a static
analyzer, a descriptor assertion, or a test-run correlator.

**backend** (term): a per-language plugin consisting of a symbol reference
scheme, a shape-hash definition, and provers.

**coverage** (term): the per-requirement evaluation of held evidence against
the policy for its clause kind and normative keyword.

**gap** (term): a committed record declaring a requirement knowingly
uncovered, stale, or broken, carrying a reason and a landing condition.

**disposition** (term): a transient operation classifying a spec edit
(editorial, split, merge, retire) and applying its evidence-inheritance
consequences to the stored records.

**tombstone** (term): the permanent registry entry for a retired identifier
or term name, preventing reuse.

**closure** (term): the transitive expansion of a requirement set over its
typed edges; the self-containedness boundary of a bundle.

**bundle** (term): a self-contained protobuf export of a requirement set and
its closure, for dissemination to agents.

## Root invariants

**REQ-core-determinism** (invariant): Given byte-identical inputs — corpus,
bindings, gap records, tombstones, and the source code under verification —
every stipulator operation MUST produce semantically identical output.

**REQ-core-claims-untrusted** (invariant): Every verification result MUST be
derived from the inputs at verification time; a persisted verification result
is never an input to verification.

**REQ-core-scope** (invariant): Stored records MUST NOT contain work
ordering, prioritization, or implementation-status narrative; state derivable
from the corpus, bindings, and code is computed on demand, never stored.

**REQ-core-proto-io** (wire): Every machine-consumed input and output of
stipulator MUST be expressible as protobuf messages; human-facing renderings
are views of those messages.

**REQ-core-vcs-free** (structural): Compilation, verification, coverage, and
diff MUST NOT depend on a version-control system; revisions enter only as
trees through the filesystem abstraction, VCS access is confined to adapter
packages, and history is never an input — rewriting history cannot change
any verification result.
