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
`stipulator://bundle/{ids}` (comma-separated identifiers, rendered as a
self-contained document), and `stipulator://coverage` (the coverage report
as JSON), with the resource list enumerating every requirement.

**REQ-mcp-tools** (wire): The server MUST expose tools `compile`, `verify`,
`gate`, `bind`, `unbind`, `gap`, and `pin`, mirroring the operation
semantics exactly, with report-shaped results rendered from the report
messages as JSON.

**REQ-mcp-writes-confined** (behavior): The server MUST NOT write outside
the record stores under `.stipulator/` — it never edits spec documents or
source code, so wiring it into any harness is low-risk by construction.

**REQ-report-messages** (wire, refines REQ-core-proto-io): Verification and
coverage reports MUST be expressible as the protobuf report messages,
carrying per-binding results, per-requirement buckets with reasons, gap
states, and the gate verdict.
