# gate/verify emit one fixed verbosity — add a view axis and a scope filter

Lands: when gate/verify output ergonomics are revisited.

`gate` (MCP) returns the full per-requirement array plus every gap on every call; `verify` (MCP)
returns every binding row. Both have one verbosity. The common questions — "did it pass, what's
red" (agent/CI) and "show me the reds" (human) — pay the full-firehose token/scroll cost every
time. It is why a consumer ends up shelling out to a compact CLI or building one: the MCP form is
unusable in a token budget, and the CLI form can't be scoped.

Two orthogonal axes fix it, shared by gate and verify so one mental model covers both:

## view — how much per item

- `summary` (proposed MCP default): just the roll-up —
  `{gate_passes, counts:{covered,uncovered,broken,stale,exempt}, violations:[ids], gaps_open:N}`.
  A few hundred bytes; answers pass/fail + what's red without the 28-row array.
- `reds` (proposed CLI default): the uncovered/broken/stale requirements with kind + reason, nothing
  covered. This is today's CLI output.
- `full`: every requirement with its bucket (today's MCP `requirements` array).
- `bindings` (verify, opt-in): the per-binding rows — the real firehose — only when asked.

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
