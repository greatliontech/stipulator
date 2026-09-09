# MCP surface

Agents consume stipulator over the Model Context Protocol: the compiled
spec as resources (agents read the IR's truth, never raw profile syntax),
and the operations as tools. The surface is observable contract — URIs and
tool names are wire, and harness compatibility rests on them. The MCP
surface outranks the CLI in design priority: it serves an LLM agent in a
harness, where every byte of output spends the consumer's context —
minimal output, maximal usefulness governs every response shape.

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
requirement as of the most recent compiling operation — reads themselves
are always fresh (each read recompiles), an unlisted-but-existing
identity still reads, and a departed one refuses: the list is a hint,
the read is the truth. Coverage deliberately has no resource: the gate tool's
views are the one surface, and a resource duplicate would be
duplication without a distinct consumer.

**REQ-mcp-tools** (wire): The server MUST expose tools `compile`, `verify`,
`gate`, `check`, `bind`, `unbind`, `gap`, `pin`, `prune`, `read_spec`,
`context`, `partitions`, `dispose`, `retarget`, `attest_requirement`,
`explain`, and `guidance`,
mirroring the
operation semantics exactly, with report-shaped results rendered from the
report messages as JSON. The `bind` tool accepts many claims
in one call, validating all-or-nothing like the gap surface.

**REQ-mcp-guidance** (behavior): Tool-level served prose MUST be the
embedded guidance document's projections (`docs/guidance.md`, in the
fleet format gofresh's guidance spec defines): every tool description
and CLI Short/Long is the document's rendering for that surface and
spelling, the server instructions are the decision map verbatim, and
the `guidance` tool (and CLI command, its verb positional) serves a
verb's full section or, verbless, the decision map — refusing an
unknown verb with the decision map named as the way to enumerate.
Both surfaces bind the per-surface coverage judgment: every listed
tool and schema property, and every visible leaf command and local
flag, documented exactly, both directions. The document's knob
prose is the authoritative superset; per-parameter schema and flag
usage strings are the document's rendering — each knob's terse first
clause, up to its first semicolon outside parentheses with its
trailing period trimmed, set at registration and never a second
literal beside the document — so a schema or usage string
cannot contradict the document, and the coverage judgment compares
the rendered text, never the names alone — cobra's help and
completion plumbing, grouping parents, the root-persistent chdir
flag, and the hidden internal resolver are surface plumbing outside
the judgment. The guidance surfaces work outside a corpus: the
document is embedded, so orientation precedes scaffolding.

**REQ-mcp-explain** (behavior): The explain verb, on both surfaces,
MUST answer a dynamic-state refusal with its derivation chain,
derived against the same policy-scoped views the verdicts derive over
(the freshness library's explain contract): given a witness's
uncacheable reason - the culprit parsed from its package-and-variable
tail - or an explicit package and symbol (a lone one refused: the
caller typed it for a reason), the MCP's structured result carries
the chain's links (kind, package, symbol, callee, clause, position)
with counted omissions, beside a one-line text digest naming the arm
and link count, and the CLI prints the same links one per line with
the omitted count. A reason naming no parseable culprit refuses with
guidance; a culprit no policy view knows answers with an empty chain,
stated as such in the digest. Views are tried in a deterministic
policy order and the first yielding a chain answers, the result
naming the answering view's invocations - a caller holding a reason
produced under a different view sees the mismatch instead of
mistaking the chain for that view's derivation.

**REQ-mcp-views** (behavior): The gate and verify tools MUST
answer at the summary view by default — the roll-up most calls want —
with richer views (per-requirement rows, per-binding rows, records with
attestation prose) and scope filters (identifiers, bucket, identifier
glob, document-or-symbol path prefix) opt-in per call — the path prefix
matches on element boundaries (equal, or the next character a `/` or
`.`), so `example.com/p` never keeps `example.com/p2` nor `docs/spec`
`docs/specs.md`, with document, symbol, and diagnostic matching sharing
the one rule, every view
rendered by one renderer per report so no two surfaces can drift, and an
unknown view or scope word refused — a typo never reads as an empty
result. A scope narrows the WHOLE report, not only its rows: the gap and
violation lists a view carries are filtered to the same requirements, so
filtered triage is never polluted by out-of-scope entries; a kept row
keeps its explaining diagnostic even when the row's own package failed to
resolve (a build-broken package's bound symbols link it on the same
element-boundary rule), so scoping onto breakage never hides the one
diagnostic that explains it. The gate
verdict a view reports stays the GLOBAL one — a scoped slice with no
in-scope violation says nothing about whether the tree passes. The check
tool answers at the summary view by default — the verdict, its evidence
class, the counts, the violations and prune residue, and the per-test
reason maps reduced to their actionable form: the top blocker reasons
by witness count, one exemplar test each, the dropped distinct-reason
count stated, never the whole histogram (the raw per-test maps ride
only the full view) — with
the full check result
message and identifier scoping opt-in per call under the same
refused-typo rule; the summary is a projection of the one result
message, never a second derivation. Check's identifier scope narrows the
pass itself, not only the view: it selects the scoped witness-evidence
class (REQ-check-verdict), so the verdict a scoped check call reports is
the flagged-partial scoped one — check's own exception to the
global-verdict rule, which continues to govern the gate's views.

**REQ-mcp-surfaces** (behavior): Per verb, each surface's default answer
MUST be derived for its reader — the MCP surface's the token-conscious
roll-up (the summary views, capped rows, the reason maps reduced to
their actionable form, the whole result only on the full view), the
CLI's the human account (the problems and red rows, the bounded
reason histograms, the counts, the progress stream rendered as status
lines with the pace line, color on a terminal) — and a knob or verb
that exists on one surface only carries the reason in the guidance
document, naming its reader, so every opt-in has a purpose or is
deleted. The readers are two, and a reason names one: the CLI's
reader is the operator at a shell and the CI and scripts behind it —
machine output and exit-code-only (`json`, `quiet`, `ir`), the
scaffolding and previews (`init`, `policy init`, `diff`, `impact`),
the server process (`mcp`), the records-only sweep (`prune`'s
`no-test`), and the shell spellings of `dispose`'s kinds; the MCP's
reader is the agent — its token economy (check's `view` and the
orientation verbs' `export_path`), its one-call all-or-nothing
authoring (`bind`'s `claims`, `dispose`'s kind form), its orientation
(`context`, `partitions`, `read_spec`), and a named parameter where
the CLI takes a positional (`guidance`'s verb). A query both readers
need is on both surfaces: "what claims this symbol" — verify's
binding rows scoped by `view`, `ids`, `filter`, and `path` — answers
the operator before a deletion exactly as it answers the agent, so
the CLI never sends an operator to grep the record files; and
`explain` derives a dynamic-state refusal for whoever holds the
reason.
Enforced by `TestGuidanceNamesTheReaderOfEverySingleSurfaceElement`.

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
structured result beside a bounded text digest (the verdict line plus
a capped action-row set with counted omissions: enough for a client
that exposes text content only to identify the file, requirement,
symbol, or record to repair, and a lossy projection, never a second
serialization of the whole payload), or, for a
document-valued result like a spec bundle, the document in the
structured result beside a size-only text digest — clients that
prefer structured content drop text blocks, so the document must ride
the channel such clients read — and one home per fact within it —
a collection travels in exactly one of the payload's messages, so a
payload embedding another report message leaves every copy of a
collection the result carries anywhere else in the payload empty. The
surfaces that grow without bound (per-test reason maps, pairwise
partition overlap terms, diagnostic collections and dossiers) travel
in full only through the full view or the export form. An empty answer
is an answer, never a bare zero-row success: an operation whose
selection matched nothing names the state that emptied it and the next
step — an unknown exact requirement identifier refuses outright and BEFORE
the pass it would scope — a typo is a refusal, never an empty result,
and never one that costs a witness run to hear — while a glob, bucket,
or path matching nothing is an informative zero — and a preview form marks
itself as such on the wire, so a zero-row check and a zero-write apply
can never be confused.

**REQ-mcp-progress** (behavior): A long-running tool call MUST report
progress as bounded notifications — the current phase, per-invocation
progress with elapsed time and counts, and the decision lines, each
one bounded line: one per executing invocation naming what executes
and the reason most of it serves no record, one per unit whose records
persisted naming it — never inside result payloads, with
a call that ends at a deadline identifying the phase in which the deadline
expired and the terminal cause, so a client can distinguish long-running
work, deadline expiry, cancellation, test failure, and server failure
without guessing. A completed suite-running call additionally stamps its
phase timings as one bounded line of its text digest — the
notification-blind fallback: a client that saw no notifications (none
requested, or dropped in transit) still distinguishes slow work from a
hang after the fact, and the one line is a timing record, not a progress
stream, so the never-inside-result-payloads rule keeps its point. An operation that exceeds its client's deadline while
reporting nothing is unusable through the agent surface even when the
identical CLI operation is healthy. A server-observed deadline expiry
carries the deadline cause; a client-side deadline surfaces as the
client's cancellation, carrying the cancellation cause and the expiring
phase, which the client composes with its own locally known reason — the
distinguishing never requires guessing. Both surfaces report from
one progress stream: the CLI renders the same events as status lines on
its error stream — each phase transition once, an invocation's
progress as completed of total packages, each decision line once — and
ends a completed run with the phase timings as its pace line and an
interrupted run with the phase it died in and what it kept, so a person
at the CLI and an agent at the server read the same account. An
interrupted CLI run then ends as what ended it: the signal, re-raised
after the ending renders, so its caller observes a signal death — or,
where a signal cannot be re-raised, the conventional status of that
signal, 128 plus its number — never a verdict's status, which a
failing verdict alone exits 1 with; a second signal during the ending
ends the process outright. The
stream is bounded by the policy — phases, invocations, persisting
units — never by the test count.
The liveness channels are bounded by the protocol: progress
notifications require a client progress token, and log-channel messages
require a client-set log level. For tokenless clients that set a level, the
server emits bounded phase-transition log messages (info, one per phase
change) so they still distinguish slow work from a hang; a
client sending neither a token nor a level has exactly the completed
call's stamps line and its own timeout policy — no server behavior can
reach it mid-call.

**REQ-mcp-cancellation** (behavior): A client cancellation MUST cancel the
underlying operation end to end, reaching package discovery and every
child process per REQ-policy-cancellation.

**REQ-mcp-writes-confined** (behavior): The server MUST NOT write outside
the record stores and the export home under `.stipulator/` — it never
edits spec documents or source code, with one exception: the enforcement
pointers a retarget's symbol rename moved, written to the corpus
document that names them and to no other path
(REQ-change-enforcement-pointers), and judged admissible for the whole
batch before any write — so wiring it into any harness is low-risk by
construction.

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
the compile outcome, the evidence class (witness-evidence,
scoped-partial with the scope echoed, or health-judged), suite health when judged, served, executed, and
uncacheable witness counts with per-test uncacheable and re-execution
reasons,
per-binding verification, coverage buckets, gap evaluation,
failed-witness diagnostics, and prune residue, with every human
rendering a projection of that message.
