# The executor folds a package's runs into an invocation two ways

Reported during package-persistence work on 2026-10-05. `assembleInvocation`
(execute.go) folds the health-judged form's runs into tests,
diagnostics, observations and per-package health, and
`SelectionResult.absorb` (selection.go) folds the selective form's
units — a process and its solo re-runs — into tests, diagnostics,
observations and process outcomes. The row/diagnostic/observation
appends are one operation written twice; the health rows and the
process outcomes are each form's own. One fold taking the unit, with
the health-judged form deriving its package health from the folded
process outcomes, would leave one place where a run's facts enter a
report.

Lands: tooling-redesign 5.3

Package completion persistence and registration/reaping of isolation processes
already exist. Fold only the common report assembly, preserving distinct
producing identities, per-group witness tiers, health dispositions, retry
durability and independently completed prefixes. Pin unequal primary/solo
populations and a later failed retry so a shared append cannot silently
overwrite earlier evidence or lose an operation account.
