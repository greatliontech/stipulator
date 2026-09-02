# `gap` accepts a non-requirement string in `covered` silently

Consumer report (bldc, 2026-08-30): three gaps declared via MCP `gap`
with free-text landing conditions in `covered` (e.g.
"the ingest plan's first chunk"). `covered` takes a requirement id;
an unknown id is filtered as dangling (GapScope) and never fires, and
`check` stays green — the gaps were permanently open with no triage
surface marking them due, until a change-set reviewer read the
records. `gap --list` now classifies them dangling, which is the
right read AFTER the fact; the ask is at declaration: refuse (or warn
on) a `covered`/`exists` value matching no requirement id in the
compiled corpus — the same fail-loud rule the tool applies to
`ScopeSubjects`' unknown ids — pointing the author at
`manual { condition: … }` for prose.

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
