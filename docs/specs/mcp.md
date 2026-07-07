# MCP surface

Agents consume stipulator over the Model Context Protocol: the compiled
spec as resources (agents read the IR's truth, never raw profile syntax),
and the operations as tools. The surface is observable contract — URIs and
tool names are wire, and harness compatibility rests on them.

**REQ-mcp-server** (behavior): Stipulator MUST provide an MCP server over
stdio exposing the compiled corpus as resources and the operations as
tools, serving fresh state per request — the corpus is recompiled and
records reloaded on every read, never cached across tree changes.

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
`gate`, `bind`, `unbind`, `gap`, `pin`, `read_spec`, `context`,
`partitions`, `dispose`, `harden`, `attest_survivor`, and
`attest_requirement`, mirroring the operation semantics exactly, with
report-shaped results rendered from the report messages as JSON.

**REQ-mcp-views** (behavior): The gate, verify, and harden tools MUST
answer at the summary view by default — the roll-up most calls want —
with richer views (per-requirement rows, per-binding rows, records with
attestation prose) and scope filters (identifiers, bucket, identifier
glob, document-or-symbol path prefix) opt-in per call, every view
rendered by one renderer per report so no two surfaces can drift, and an
unknown view or scope word refused — a typo never reads as an empty
result.

**REQ-mcp-writes-confined** (behavior): The server MUST NOT write outside
the record stores under `.stipulator/` — it never edits spec documents or
source code, so wiring it into any harness is low-risk by construction.

**REQ-report-messages** (wire, refines REQ-core-proto-io): Verification and
coverage reports MUST be expressible as the protobuf report messages,
carrying per-binding results, per-requirement buckets with reasons, gap
states, and the gate verdict.
