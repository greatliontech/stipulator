# MCP structured results are invisible to an OpenCode agent client

Lands: when a live OpenCode client can consume Stipulator's structured tool
results, or the MCP response contract provides a bounded actionable fallback
for clients that expose text content only.

## Observed

Live dogfood on 2026-07-24 used Stipulator's MCP tools from OpenCode while
repairing bindings in the VMM corpus. The server returned its one-line text
summary, but the client exposed no structured payload to the agent:

- `compile` reported `compile: 3 diagnostics` without diagnostic locations or
  messages; the CLI named all three source lines and causes;
- `gate` with `view=reds` reported only
  `gate (structured content carries the payload)`;
- `verify` with `view=bindings` reported only
  `verify (structured content carries the payload)`;
- `context` reported only a dossier count;
- detailed requirement identifiers, buckets, stale symbols, reasons, and
  prune residue were recoverable only by running the CLI with `--json` and
  filtering its output.

The MCP calls completed successfully. This is not a server timeout, witness
failure, or oversized-result truncation. The agent client rendered the text
content but did not make the result's `structuredContent` available to the
model.

## Why it matters

REQ-mcp-response-contract deliberately carries the payload exactly once in
`structuredContent` beside a one-line text summary. For a client that exposes
only text content, every rich MCP view becomes non-actionable even when its
bounded structured result is correct. The agent cannot identify the file,
requirement, symbol, or record it must repair and must leave the MCP surface
for the CLI. This defeats the agent-first purpose of REQ-mcp-server without
producing an explicit compatibility error.

The failure also makes a successful tool result look like an internal adapter
placeholder: `structured content carries the payload` tells the agent where
the answer exists but supplies no way to read it.

## Resolution

Verify the server response against a live OpenCode client and determine which
boundary drops or hides `structuredContent`:

1. If OpenCode supports the MCP field but the harness adapter fails to expose
   it, fix the adapter and add an end-to-end client fixture covering compile
   diagnostics plus one rich report view.
2. If this client class cannot consume `structuredContent`, decide the response
   contract explicitly: require compatible clients and return a detectable
   incompatibility, or provide bounded actionable text rows without duplicating
   the full payload.
3. Exercise `compile`, `gate(view=reds)`, `verify(view=bindings)`, and `context`
   through the live client. Each must expose the identifiers and reasons needed
   for the next repair without a CLI fallback.

This confirms the structured-result concern already noted as an unverified
part of `mcp-progress-not-observed.md`; progress notification behavior remains
a separate observation.
