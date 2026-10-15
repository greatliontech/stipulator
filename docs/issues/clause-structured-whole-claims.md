# Default refusal of whole claims on clause-structured requirements

Lands: tooling-redesign 5.3

REQ-evidence-clause-claim currently permits a claim without a clause to grant
its evidence to every payload clause. `internal/records` resolves clause
scope and `internal/coverage` applies that rule. Accepting whole claims is
legal under today's contract, not an implementation regression.

The 2026-09-07 ruling selects refusal by default, with a manifest opt-out,
for whole-requirement `tests` and `proves` claims on clause-structured
requirements. `implements` claims remain whole. The remaining obligation
is a deliberate contract amendment and coordinated authoring, hygiene and
coverage change, including existing records and the opt-out's meaning.
Pin ordinary scoped claims and explicit opt-out behavior; a test witnessing
one clause must not gain all clauses merely through an authoring default.
