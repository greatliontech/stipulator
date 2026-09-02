# Coverage's unit is the requirement, so a multi-clause requirement reads green with a clause unenforced

Consumer report (bldc, 2026-08-29, the repo's own H-graded false-green
channel): a binding claim names a requirement, and coverage judges
per requirement, so a requirement with four MUST-clauses reads
covered when one clause has a witness. bldc's answer was a one-time
clause-coverage sweep of the whole corpus (2026-08-29) and a policy
of splitting on demonstrated need thereafter — the adversarial loop's
per-change-set clause walk as the incremental detector. The upstream
ask that would retire that manual discipline: clause-granular
claims — a binding naming the clause it witnesses (by ordinal, or a
clause label the spec format admits), with coverage reporting the
unclaimed clauses of an otherwise-bound requirement as a distinct
bucket ("bound, clauses unclaimed") rather than green.

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
