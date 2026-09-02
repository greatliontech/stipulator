# The symbol-claims query is MCP-only: CLI `verify` has no `--view`/`--path`, and no `explain`

Consumer report (bldc, 2026-08-30): deleting an exported symbol needs
"what claims this?" answered BEFORE the deletion. The capability
exists on the MCP face (`verify` with `view=bindings`,
`path=<symbol>` returns the claiming rows) but the CLI `verify` takes
only `--no-test` and there is no CLI `explain`, so a CLI-driven
workflow falls back to `grep -o 'pkg\.[A-Za-z0-9_]*'
.stipulator/bindings/*.textproto` — a grep over an internal format.
Met when `kernel.Round` looked dead by every source-level measure and
was bound as an IMPLEMENTS symbol; `check` refused after the fact.
Ask: surface `--view` and `--path` on CLI `verify` (and the
`explain` verb), so the pre-deletion check is a query.

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
