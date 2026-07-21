# MCP surface

Agents consume stipulator over the Model Context Protocol: the compiled
spec as resources (agents read the IR's truth, never raw profile syntax),
and the operations as tools. The surface is observable contract — URIs and
tool names are wire, and harness compatibility rests on them.

**REQ-mcp-server** (behavior): Stipulator MUST provide an MCP server over
stdio exposing the compiled corpus as resources and the operations as
tools, serving fresh state per request — the corpus is recompiled and
records reloaded on every read, never cached across tree changes — and
declaring server instructions that teach an agent which tool answers
which question, so tool selection needs no trial calls. A tool invoked
outside any corpus fails with the same guided root-discovery message
the CLI gives: the upward search that ran, and the init pointer.

**REQ-mcp-resources** (wire): The server MUST expose resource URIs
`stipulator://req/{id}` (a requirement's compiled view: canonical text,
kind, keyword, content hash, edges, source), `stipulator://term/{name}`,
and `stipulator://bundle/{ids}` (comma-separated identifiers, rendered
as a self-contained document), with the resource list enumerating every
requirement as of the most recent operation — reads themselves are
always fresh. Coverage deliberately has no resource: the gate tool's
views are the one surface, and a resource duplicate would be
duplication without a distinct consumer.

**REQ-mcp-tools** (wire): The server MUST expose tools `compile`, `verify`,
`gate`, `check`, `bind`, `unbind`, `gap`, `pin`, `prune`, `read_spec`,
`context`, `partitions`, `dispose`, `targets`, and `attest_requirement`,
mirroring the
operation semantics exactly, with report-shaped results rendered from the
report messages as JSON. The `targets` tool accepts arrays of exact requirement
identifiers, implementation backends, and implementation symbols; it has no
staged-diff input and returns `BindingSurfaceReport` as a structured
result, with an opt-in caller-named export path writing the identical
structure to a file — the artifact handoff that spares a consuming tool
the inline copy of a large surface. The `bind` tool accepts many claims
in one call, validating all-or-nothing like the gap surface.

**REQ-mcp-views** (behavior): The gate and verify tools MUST
answer at the summary view by default — the roll-up most calls want —
with richer views (per-requirement rows, per-binding rows, records with
attestation prose) and scope filters (identifiers, bucket, identifier
glob, document-or-symbol path prefix) opt-in per call, every view
rendered by one renderer per report so no two surfaces can drift, and an
unknown view or scope word refused — a typo never reads as an empty
result. A scope narrows the WHOLE report, not only its rows: the gap and
violation lists a view carries are filtered to the same requirements, so
filtered triage is never polluted by out-of-scope entries. The gate
verdict a view reports stays the GLOBAL one — a scoped slice with no
in-scope violation says nothing about whether the tree passes. The check
tool answers at the summary view by default — the verdict, its evidence
class, the counts, the violations and prune residue, and the reason
maps aggregated to bounded histograms — with the full check result
message and identifier scoping opt-in per call under the same
refused-typo rule; the summary is a projection of the one result
message, never a second derivation.

**REQ-mcp-response-contract** (behavior): Every tool result MUST fit a
declared budget by construction, in one of four forms: a bounded
projection (summary-first defaults, capped lists whose omitted
remainders are counted — a truncation is never silent), a caller-named
export under the record-store home carrying the full document with only
its location on the wire, a payload bounded by the caller's own
explicit identifier list, or a payload proportional to the committed
corpus and records — the caller's own artifact bounds it. What no
result may scale with is a runtime product: test output volumes,
failure counts, or pairwise combinations grow without the caller
having authored anything, and those surfaces take a cap or an export,
never a passthrough. One wire encoding of the payload — the
structured result beside a one-line text summary, or, for a
document-valued result like a spec bundle, the document as the text
content beside a size-only structured result — never a duplicate
serialization of the whole payload, and one home per fact within it —
a collection travels in exactly one of the payload's messages, so a
payload embedding another report message leaves every copy of a
collection the result carries anywhere else in the payload empty. The
surfaces that grow without bound (per-test reason maps, pairwise
partition overlap terms, diagnostic collections and dossiers) travel
in full only through the full view or the export form.

**REQ-mcp-progress** (behavior): A long-running tool call MUST report
progress as bounded notifications — the current phase, and per-invocation
progress with elapsed time and counts — never inside result payloads, with
a call that ends at a deadline identifying the phase in which the deadline
expired and the terminal cause, so a client can distinguish long-running
work, deadline expiry, cancellation, test failure, and server failure
without guessing. An operation that exceeds its client's deadline while
reporting nothing is unusable through the agent surface even when the
identical CLI operation is healthy. A server-observed deadline expiry
carries the deadline cause; a client-side deadline surfaces as the
client's cancellation, carrying the cancellation cause and the expiring
phase, which the client composes with its own locally known reason — the
distinguishing never requires guessing.

**REQ-mcp-cancellation** (behavior): A client cancellation MUST cancel the
underlying operation end to end, reaching package discovery and every
child process per REQ-policy-cancellation.

**REQ-mcp-writes-confined** (behavior): The server MUST NOT write outside
the record stores and the export home under `.stipulator/` — it never
edits spec documents or source code, so wiring it into any harness is
low-risk by construction.

**REQ-report-messages** (wire, refines REQ-core-proto-io): Verification and
coverage reports MUST be expressible as the protobuf report messages,
carrying per-binding results, per-requirement buckets with reasons, gap
states, and the gate verdict.

**REQ-report-policy-messages** (wire, refines REQ-core-proto-io): The
accepted test policy and its execution report MUST be expressible as
protobuf messages — backend-neutral invocation envelopes carrying typed
backend payloads, canonical invocation identity, per-invocation and
per-package health dispositions, completeness omissions, progress events,
and failure diagnostics.

**REQ-report-check-result** (wire, refines REQ-report-messages): The
unified check operation MUST return one protobuf check result carrying
the compile outcome, the evidence class (witness-evidence or
health-judged), suite health when judged, served, executed, and
uncacheable witness counts with per-test uncacheable and re-execution
reasons,
per-binding verification, coverage buckets, gap evaluation,
failed-witness diagnostics, and prune residue, with every human
rendering a projection of that message.
