# Multiply-selected witnesses are uncacheable — every check re-executes the whole corpus

Field-proven on tugboat (2026-08-13, standing since): 1050/1050
witnesses report uncacheable with "multiply-selected: a record has no
per-invocation identity", so every `stipulator check` re-executes the
full corpus — ~25 minutes on a warm tree that changed one file. The
serve-fresh-execute-stale design is disarmed entirely: "0 witnesses
served, 1067 executed" is the steady state (tugboat gap evaluation,
2026-08-24). The consumer worked around only by scheduling (checks
backgrounded, pipelined against review rounds) — the cost lands on
every change set of every adopting project.

The cause as reported by explain: a witness selected by more than one
requirement loses per-invocation identity, and without identity no
cached verdict can be attributed, so the whole class is conservatively
re-executed. Multiply-selected witnesses are the NORM in a real
corpus (a good test pins several requirements), so the conservative
class is effectively everything.

Fix shape: give a witness invocation an identity independent of its
selecting requirement set (the witness IS one execution of one test
symbol against one tree state; which requirements cite it is
attribution, not identity), so its verdict caches once and serves
every citer.

Lands: with the tool-phase stipulator visit (fleet development is
paused on validation-tool health, 2026-08-24 — this is the
efficiency half of that decision).
