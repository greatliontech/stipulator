# The one-normative-keyword rule surfaces at the next write, and its message names the count, not the remedy

Consumer report (bldc, 2026-08-29 and 2026-08-30): the
one-normative-keyword-per-requirement rule is checked when the corpus
next compiles, so an amendment's violation surfaced only when a later
`gap` declare failed — the error was good (file, line, count,
expectation) but landed on an unrelated operation. And the message
("has 3 normative keyword occurrences, want exactly 1",
compile.go) names the count where the remedy is the useful half:
"split the clauses into their own requirements, or coordinate them
under one keyword". The refusal itself was right — it caught a
four-clause MUST whose clauses had four enforcement states.

Asks: a lint entry point (or a check at `pin --req` time) that
compiles the corpus at the amendment; and the remedy in the message.

Lands: cross-tool train chunk 144 (gofresh docs/plans/cross-tool-train.md; bldc consumer report 2026-09-02).
