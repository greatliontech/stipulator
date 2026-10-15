# An empty-label claim's remedy selects more than the malformed claim

Lands: tooling-redesign 5.3

`internal/records`' `ResolveClause` refuses a hand-written
`clause_label: ""`, but its empty spelling gives the hygiene remedy no
`--clause` selector. `stipulator unbind --req X --symbol Y` then selects
every claim on that pair, not just the malformed one. No tool authors an
empty label; bind's empty clause text means a whole claim.

The remaining obligation is to classify this external record as malformed
and name its record path as the remedy, never a selector wider than the
offending claim. The 2026-09-29 derivation requires no empty-label authoring
form. Pin the remedy with valid sibling claims on the same pair and preserve
ordinary clause-specific unbinding.
