# Bindings, evidence, coverage

The claim/evidence split is the trust boundary of the system: bindings are
committed, reviewable claims authored by humans or agents; evidence exists
only as the output of stipulator verifying those claims against the current
corpus and code. Nothing written into the record stores can make a
requirement covered. The spec corpus and the manifest — including its
coverage-policy overrides — stand outside that boundary as contract-tier
configuration: editing them changes what coverage means, which is why they
are reviewed like spec text and why every active policy override is
surfaced in coverage output rather than applied silently. An attestation
record is the one record that carries judgment: it can render a
requirement attested — never covered — and only where the policy admits
it.

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
content pins and shape pins in its blanket form — a differing content pin
is never rewritten without naming the requirement, so staleness cannot be
laundered by a blanket re-pin. Naming requirements explicitly is the
editorial re-consent (REQ-change-editorial), surfaced under pin as well
as the dispose verb, and a pin invocation that changes nothing reports
the no-op rather than returning silence.

**REQ-evidence-record-verbs** (behavior): Binding records MUST be writable
through tool operations that validate at write time — the requirement
against the compiled corpus, the symbol through its backend when one exists
— rendering through the machine-owned writer with the content pin, and the
shape pin when the backend has a verifier, applied immediately, so a freshly
authored claim is never born stale.

**REQ-evidence-binding-roles** (behavior): A binding's role MUST be one of
`implements` (the symbol realizes the requirement), `tests` (the symbol is a
test exercising it), or `proves` (a backend prover assertion checks it).

**REQ-evidence-proves-discharge** (behavior): A `proves`-role binding MUST
be rejected at write time unless the backend resolves the bound symbol as
an analyzer assertion it can discharge — a proof claim that can never
produce evidence is refused, not recorded to fail silently.

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

**REQ-evidence-witness** (behavior): A witness MUST record that a named
test passed in the current verification run while bound to the
requirement. Runtime registrations refine attribution — which subtest
claims which requirement, cross-checked against the binding store and
reported with per-subtest outcomes — but evidence follows the bound
test's outcome alone: a passing subtest inside a failing test binary
grants nothing, because a red suite never yields green evidence.

**REQ-evidence-run-attributes** (behavior): A witness MUST carry the rigor
attributes of the run that produced it, at minimum whether the race
detector was enabled.

**REQ-evidence-witness-freshness** (behavior): A witnessing run MAY serve a
test's outcome — its subtest outcomes and runtime registrations riding with
it — from a local cache exactly when the freshness fingerprint recorded
beside it checks valid against the current tree, because a valid fingerprint
proves the test's source closure and produced environment are those that
produced the outcome: the served outcome is the current run's verification
by proven equivalence, not a trust extension, so REQ-evidence-promotion
holds. Anything short of valid — a stale or unverifiable verdict, an absent
or unreadable record — runs the test; absence of proof never serves an
outcome. The fingerprint pins the closure and environment guards with the
race flag as a caller-supplied build input, and the run's observed
runtime-input manifest is captured per package under the same environment
as the witness invocation and attached to every test fingerprinted from
that run — an over-approximation whose failure direction is a spurious
re-run, never a spurious reuse, with one declared exception: the
observation excludes the repository root listing and the VCS bookkeeping
tree (`.` and `.git`), whose digests move under unrelated tooling and are
asserted to be no witness's input. The exclusion carries the caller-side
soundness responsibility gofresh's exclusion contract assigns it — its
failure direction is a spurious reuse, accepted exactly there and — each
scoped to the stated assumption whose violation would convert it — in
the exemption classes below, nowhere else. Each producing process's completed observation is sealed against an
observation bracket captured before the process spawns, declaring the
package's own directory — module-relative under the verification tree,
with the VCS bookkeeping tree excluded — together with the invocation's
reviewed bracket paths (process images and fixed external files its
tests deliberately consume, each a clean absolute or tree-relative
path, fingerprinted present or absent) as its roots: a change under
any declared root persisting across the run-to-ingest span moves the
bracket — a restore is tolerated only when it reproduces content and
metadata alike — and the observation seals unverifiable, while a read
resolving outside the declared root seals per-identity unverifiable —
permanently uncacheable under this root policy — both toward
re-execution, never reuse. Three classes are exempt: a read resolving
under the effective toolchain root; under the module cache outside its
download-cache subtree; or under the effective build cache outside its
fuzz-corpus subtree — each classifies guard-covered. The toolchain
guard pins the toolchain root's contents, version-addressed
immutability pins module trees, and build-cache reads are admitted on
toolchain-mediated observational equivalence — the toolchain rederives
or revalidates cache content from inputs the fingerprint already pins —
under gofresh's runtime-inputs contract, whose stated assumption
excludes a subject consuming cache objects as opaque data. Observing an
exempt read neither enters the manifest nor seals anything, and a
toolchain- or cache-reading witness stays cacheable. The download-cache
subtree's mutable metadata and the fuzz-corpus subtree's independently
grown evidence are pinned by nothing and stay observed. The producing
environment's temp root is declared ephemeral: the root's own identity
admits without entering the manifest — its churn between runs is
asserted to be no witness's input — while any read beneath it stays
observed. The kernel's stable machine-fact identities enter the
manifest as the machine guard's stable projection rather than volatile
content, so a machine-fact-reading witness stales on a hardware or
kernel change, with the in-window residual gofresh's runtime-inputs
contract states. A package whose directory is unresolved before
spawn, or whose directory lies outside the verification tree, yields an
incomplete observation rather than a completed record sealed without a
bracket. Executed tests whose records cannot be published for reuse — and
expected witness subjects a run denied execution outright — are
reported beside the run/served summary with a per-test attributable
reason — the sealed observation's own reason, the refused proof's, the
missing granting process, the post-run drift with its moved inputs
named, whichever leg refused — so a shrinking cache is a visible,
diagnosable set, never silence and never a bare number. A re-executed
test that held prior witness evidence names why serving refused it —
the stale variant's verdict with its moved inputs — while a test with
no prior record needs no reason beyond the absence. Human renderings
aggregate the reasons; the per-test attribution rides the machine
result. Witness packages
execute concurrently under a processor-count bound, which assumes what
standard Go tooling already assumes of them (`go test` runs packages
in parallel by default): witnesses do not mutate inputs other packages
observe. A suite violating that is caught whenever the interference
persists across the interfered process's run-to-ingest span: a
persisting change under the record's declared bracket root moves its
bracket, a write outside every declared root can interfere only
through inputs whose reads already seal uncovered, and interference
with a served record's observed inputs surfaces at its post-run
revalidation — the interfered records re-execute rather than reuse. A
mutation-and-restore interval completed exactly within the span —
content and metadata alike — is the residual gofresh's
observation-coherence contract declares unprovable: records interfered
with that way forfeit the spurious-reuse guarantee, exactly as an
ambient mid-run edit-and-restore does. No serial-order control survives the accepted
record; diagnostics narrow instead of serialize: re-running the
suspect subjects alone is a witness-only selective execution, and each
solo subject runs in a process of its own. A selective run may
isolate a test its process siblings would otherwise deny an outcome: a test
shadowed by a package abort, or a completed pass inside a process whose own
disposition is red — a red process yields no green evidence, so the pass
grants nothing from that process. The isolated outcome is a real run's
outcome — evidence follows execution, the aborting or failing sibling's own
failure stands, and a test gaining its outcome this way is the selective
form being more precise, not less. A served record's fingerprint is
revalidated after the run's executions complete: a served outcome whose
record no longer checks valid is discarded and its subject executed once
within the same run, and a still-drifting subject ends unwitnessed with
its record dropped and counted uncacheable — the run's evidence never
reports a serve the tree it finished on disproves. The cache is memoization,
never authoritative and never committed: for a deterministic test,
discarding it changes no verdict, only the work — a flaky test's served
outcome is that flake pinned until its inputs move or the cache is
discarded, which is a finding about the test, not the cache. A test whose
fixture reads leave it unverifiable re-runs every time until its author
asserts purity in source, the deliberate opt-in. A clean witness invocation
instead may publish without that assertion when its completed testlog is
attached to a compatible caller-selected Gofresh observation-completeness proof
captured before execution and both are revalidated after execution. Stipulator
selects that proof only when the producing test process runs exactly one
selected top-level runnable — executable examples counted among them — so no
sibling runnable in the process can contribute unrecorded process state to
the subject's outcome; an ordinary freshness check never infers proof selection.

**REQ-evidence-witness-cache-format** (behavior): The local witness cache MUST live
outside the repository, under the user cache directory keyed by the corpus root's
absolute path, as one JSON file per record variant — named by the record identity's
digest joined with its fingerprint's digest, each name segment the first
sixteen hexadecimal characters of the REQ-model-hash-func digest — the
identity segment over the package, a NUL byte, and the test name; the
fingerprint segment over the fingerprint's canonical JSON encoding; and
the store's per-corpus directory the same truncation over the resolved
corpus root's absolute path: a filename-length economy over the one
hash the model defines, never a second hash function, with the per-file
name-content agreement check absorbing the truncation's collision risk. The fingerprint's own 16-byte
digests are Gofresh-owned integrity values, outside REQ-model-hash-func
entirely. Files install atomically, so distinct tree
states of one test coexist as variants and alternating between branches evicts
nothing. Each file carries one record object with integer `version` equal to `4`,
string `package` and `test`, object `fingerprint`, object `outcomes`, and optional
array `registrations`. Its fingerprint keys are `maximalClosure`, `toolchain`,
`buildConfig`, an optional `observationAssertion` plus `observationProof` pair, and
optional `purityAssertion`,
`runtimeInputs`, `runtimeDigest`, and numeric `resultKind`; closure, build, and runtime
digests are 16-byte lowercase hexadecimal values, the observation assertion and proof
are structurally encoded attributable Gofresh evidence for the record's subject, the runtime
manifest is canonical Gofresh v1, purity is empty or a recognized Gofresh attribution,
measurement fields are absent, and result kind is Gofresh code-result. An
`observationProof` object has string keys `strategy`, `package`, `symbol`, optional
`reason`, and `evidence`, plus required non-null boolean `observable`; its package and symbol equal the
record identity, `reason` is absent exactly when `observable` is true, and `evidence`
is a 16-byte lowercase hexadecimal Gofresh integrity digest. Every outcomes
object contains its record's top-level `package.test` key and
only that key or its `/subtest` descendants, with `passed`, `failed`, or `skipped`
values. Cache deserialization validates canonical structural encoding and proof
disposition consistency only. Source-bound proof integrity and compatibility require
the current Gofresh view and are enforced by `CheckObserved`, so a structurally valid
but incompatible historical proof remains readable but cannot grant reuse. Optional
fields are omitted rather than encoded as `null`. Unknown fields, another version,
a record disagreeing with the file's own name, or any structural malformation
makes that file alone an absent record — sibling records stay trusted, because
refusal is per record and costs only that record's execution, while a record is
never migrated or partially trusted. Per identity the store keeps a bounded set
of the most recently installed variants; eviction is by recency and costs only
execution.

**REQ-evidence-freshness-no-health** (behavior): A freshness-served test
outcome MUST NOT contribute to package, command, or suite health; serving
grants witness evidence for the served test alone, and every health
disposition comes from current execution of its producing invocation —
proven equivalence covers one test's outcome, never whether a whole
command would build, start, and exit cleanly today.

**REQ-evidence-freshness-degrade** (behavior): A fault anywhere on the
freshness path MUST degrade to the full witnessing run: the cache saves
work, it never blocks or weakens witnessing. On the witness-only
selective path the full witnessing run is that path with an empty served
set: every subject the accepted policy covers executes under its
covering invocation, and work the policy leaves outside witnessing stays
outside, degraded or not.

**REQ-evidence-attestation** (behavior): An attestation MUST carry its reason
text and appear distinctly in every coverage output; it is the weakest
evidence and is never silently aggregated into stronger kinds. A
requirement cannot carry both a gap and an attestation — deferred and
judged-satisfied contradict, and verification fails on the pair.

## Test policy

The accepted test policy is contract-tier configuration exactly as the
manifest is: a committed record of which test commands constitute a
complete suite for this tree, reviewed like spec text. It exists because
neither extreme is sound. An imposed universal invocation misstates
corpora whose accepted rigor differs per package — racing selected
packages while an analysis-heavy package runs uninstrumented is a
legitimate accepted policy, not a weakened one. Running only the named
bound tests silently drops the rest of a real suite: build failures,
initialization failures, executable examples, committed seed replay,
packages with no named test, and workspace members with no binding at
all are part of what a suite checks. The policy names the whole suite
explicitly so that one execution serves both consumers — suite health
for the change gate and witness evidence for coverage — instead of a
health suite whose outcomes are discarded beside an independent witness
run duplicating its work.

**REQ-policy-explicit** (behavior): Witness execution MUST consume the
corpus's accepted test policy — a committed record declaring every policy
invocation with its backend, package scope, typed configuration, and an
explicit timeout, so a deliberately long-running invocation is admitted by
review rather than aborted by an inherited ceiling — never an assumed
universal invocation. No toolchain-implicit time bound survives the
accepted record: the record's envelope and its reviewed arguments are the
only sources of execution bounds, so an inherited default can never abort
work the record admitted.

**REQ-policy-record-location** (wire): The accepted test policy MUST be
stored as textproto at `.stipulator/policy.textproto` in canonical form:
at least one invocation declared — a record accepting no test work names
no suite whose health a run could judge, so admitting it would create a
tree that fails forever without a stated cause — with invocation names
non-empty, unique, and strictly ascending in byte order,
each invocation carrying exactly one typed backend payload and a valid,
positive explicit timeout. A record violating canonical form — unknown fields,
duplicates, and out-of-order or incomplete invocations included — is
refused whole, never reordered, defaulted, or partially loaded: the record
is reviewed contract, and what runs must be what was reviewed.

**REQ-policy-init-immutable** (behavior): `stipulator policy init` MUST NOT
modify an existing policy record: absent, it writes the derived record and
reports the configuration break; byte-identical, it succeeds as a no-op;
divergent, it fails naming the first differing line — an accepted record
changes only by review, never by derivation.

**REQ-policy-backend-neutral** (structural, refines
REQ-backend-core-neutral): The core policy model MUST treat each policy
invocation's backend configuration as an opaque typed payload dispatched
to the named backend; orchestration observes only canonical invocation
identity and health facts.

**REQ-policy-conservation** (behavior): One execution of the accepted test
policy MUST preserve every failure class and selection obligation its
backends define for a complete suite, reporting every obligation the
policy omits or selects more than once — an omission is a visible fact,
never silence.

**REQ-policy-attribution** (invariant): Every test outcome and every
runtime observation MUST be bound to the exact policy invocation and
operating-system process that produced it — including the invocation's
resolved configuration: a field the committed record leaves absent pins its
effective value at load, and that resolved value is part of the invocation's
evidentiary record, so what actually ran is reviewable after the fact.
Outcomes or observations from distinct processes are never merged into one
evidentiary record.

**REQ-policy-cancellation** (behavior): A cancelled policy execution MUST
discard its partial results — no outcome, observation, or health
disposition from a cancelled run is persisted, served, or reported as
terminal — with cancellation propagated to every child process of the
execution, package discovery included.

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
