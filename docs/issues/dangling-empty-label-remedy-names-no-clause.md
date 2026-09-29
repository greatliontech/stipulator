# The remedy for a dangling empty-label claim names no clause

Hygiene names a claim whose clause the requirement does not declare
"dangling" and prints an unbind remedy composed from the claim's clause
spelling. A hand-written record carrying `clause_label: ""` names no
clause (records.ResolveClause refuses an empty label), but its spelling
is the empty string, so the printed remedy is `stipulator unbind --req
X --symbol Y` with no `--clause` — and that selector removes every
claim on the requirement and symbol, not the dangling one. No tool
writes an empty label (`bind` clears the clause on empty text), so the
shape is reachable through hand-written records alone; `unbind` has no
form that names an empty label, so the repair is a remedy-vocabulary
question — a selector for "the empty label", or a refusal to compose a
remedy for it — not a hygiene fix.

Lands: cross-tool train chunk 176 (derived 2026-09-29: no tool writes an
empty label, so a record carrying one is malformed external input —
hygiene classifies it as malformed, naming the record path as the
remedy, never a selector wider than the claim; `unbind` gains no
empty-label form).
