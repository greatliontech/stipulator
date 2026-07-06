# context returns nothing — make it the per-requirement dossier

Lands: when the MCP read surface is next revisited (companion to
gate-verify-output-granularity, which reshapes the same surface).

`context` (MCP) is the natural orientation call — "tell me everything about REQ-X" — and
today it answers nothing:

- with a valid single requirement id it returns `{}`;
- with a JSON-array-encoded `ids` value the array arrives as one string and errors
  `["REQ-corpus-two-stage" is not in the corpus` — a list-encoding gap wearing a mangled
  error message.

Meanwhile the questions an agent actually asks when orienting on one requirement have no
home on the MCP surface, tool or resource:

- why is this gap open — the gap record's `reason` and `lands` live only in
  `.stipulator/gaps/*.textproto`;
- which symbols are bound, in which roles, with which witness classes and pin freshness —
  only in `.stipulator/bindings/*.textproto`;
- what hardening state the implementing symbols carry — kills, survivors, attestations —
  only in `.stipulator/hardening/`.

A consumer ends up cat-ing textprotos, which makes the on-disk layout the de-facto API.
The files are deliberately human-readable, but read paths the verbs conceptually own
should not require knowing the record store's directory structure.

Proposal: `context(ids...)` returns, per id, the dossier — clause text with kind and
keyword; coverage bucket with reasons; the gap record (state, reason, lands) if one is
open; bindings as (symbol, role, witness class, pin fresh/stale); and a hardening roll-up
(mutants, killed, survivor count, attested count) for bound implementing symbols. Fix the
`ids` list encoding, and make unknown-id errors quote the offending id cleanly.

## Tools are the primary surface; resources mirror them

Placement verdict, so the dossier does not land resource-only: the MCP specification
makes tools model-controlled and resources application-driven, and the major clients
follow it — Claude Code, Claude Desktop, and claude.ai all expose resources to the model
only through user attachment (in Claude Code, `@server:uri` mentions or deferred
list/read tools the model must already think to call). An agent orienting mid-task
discovers tools; it does not discover resources. So: anything an agent needs on the hot
path must be reachable through a tool; resources stay valuable as the user-attachment
lane (`@stipulator:stipulator://req/REQ-x` dropping a clause into context is exactly
right) and as browsable mirrors — rendered by the same code as the tool views, so the
two can never drift. The current `stipulator://coverage` resource, a byte-identical copy
of `gate` output, is the shape to avoid: duplication without a distinct consumer.
