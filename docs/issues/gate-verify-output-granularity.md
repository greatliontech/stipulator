# gate/verify/harden emit one fixed verbosity — add a view axis and a scope filter

Lands: when gate/verify output ergonomics are revisited.

`gate` (MCP) returns the full per-requirement array plus every gap on every call; `verify` (MCP)
returns every binding row; `harden` (MCP) returns every cached record with its full attestation
prose even when nothing ran. All three have one verbosity. The common questions — "did it pass,
what's red" (agent/CI) and "show me the reds" (human) — pay the full-firehose token/scroll cost
every time. It is why a consumer ends up shelling out to a compact CLI or building one: the MCP
form is unusable in a token budget, and the CLI form can't be scoped.

Measured in a consuming corpus (29 requirements, 8 hardened symbols): a gate call is ~3.5k
tokens, verify ~5k, harden ~4k — an orientation pass of a few calls burns ~20k tokens, and the
bulk is constant boilerplate (per-row `path`, `backend`, `contentPinned` identical throughout)
plus attestation reasons replayed from cache.

Two orthogonal axes fix it, shared by gate, verify, and harden so one mental model covers all:

## view — how much per item

- `summary` (proposed MCP default): just the roll-up —
  `{gate_passes, counts:{covered,uncovered,broken,stale,exempt}, violations:[ids], gaps_open:N}`.
  A few hundred bytes; answers pass/fail + what's red without the 28-row array.
- `reds` (proposed CLI default): the uncovered/broken/stale requirements with kind + reason, nothing
  covered. This is today's CLI output.
- `full`: every requirement with its bucket (today's MCP `requirements` array).
- `bindings` (verify, opt-in): the per-binding rows — the real firehose — only when asked.
- `harden` maps onto the same axis: `summary` = per-symbol counts (mutants, killed, survivors,
  attested) plus only the *unattested* survivors; `full` = today's records with attestation
  prose, opt-in.

## scope — which items

- `ids`: comma-separated requirement ids (already exists on read_spec/context — mirror it here).
- `bucket`: `uncovered|covered|broken|stale|exempt` — "show me only the broken ones".
- `filter`: requirement-id prefix/glob, e.g. `REQ-arch-*`.
- `path`: a spec file or package/dir prefix — "coverage for just what I'm working on"
  (`docs/specs/architecture.md`, `internal/corpus`). Scopes the walk to that slice.

## surface

- MCP `gate` / `verify`: add `view` (default `summary`) and the scope params. `summary` alone
  removes the reason a caller shells out.
- CLI `stipulator gate` / `verify`: `--summary`/`-s`, `--all`, `--bucket`, `--id` (repeatable) /
  `--filter`, `--path`, `--bindings`, `--json` (machine form mirroring the MCP shapes), `--quiet`/`-q`
  (exit code only, for CI). Keep the pass/fail exit code.

The single highest-value change is the MCP `summary` default — it is the answer 90% of calls want,
and it is the one shape neither surface offers today.

The `stipulator://coverage` resource currently duplicates gate's full output byte for byte. When
the view axis lands, the resource should serve the rendered views (summary by default) through
the same renderer as the tool, so the two surfaces cannot drift and the duplication buys a
distinct consumer (user attachment) instead of nothing.
