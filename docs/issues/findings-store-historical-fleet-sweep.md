# Findings store reads historical under the oracle-execution-policy judgment (fleet sweep 2026-10-12)

The weekly fleet sweep's store inspection reports this repo's
`.gomutant` store (28K: three machine-local records —
`internal/author.Gap` with 24 open survivors among them — and two
ephemeral equivalence attestations beside them) as

    oracle execution policy "": oracle execution policy is missing or
    unsupported; re-measure with the complete oracle; counts are
    historical — 0 repo-committable, 3 machine-local

The installed gomutant now stamps every record with the oracle
execution policy its measurement ran under and lends no authority to
a record lacking one (the negative-coverage cutoff: historical banks
and findings barred from granting exemptions or reuse without sound
re-measurement — tooling-redesign 1.3, landed). These records predate
the policy, so their survivor counts are history, not a current
verdict, until re-measured under the complete oracle. Nothing
committed depends on them (0 repo-committable), but the open
survivors they hold stay undispositioned until that re-measure — and
a campaign over stipulator's own face is not run as a measurement
(the train's standing rule), so the re-measure is a named authorized
scope, not a chunk gate.

Scanned: the index and all 33 docs it lists. None tracks the findings
store. The same line fires this week on tugboat's, pando's and
protodb's stores, each widened into the doc that already schedules
its re-measure; the gomutant side is the landed repair, not a defect.
Rows disjoint; no scheduled trigger duplicated.

Lands: tooling-redesign 9.1 (the reconciliation of every earlier
campaign scope under the repaired oracle) — the three records
re-measured at 9.3's authorized final scopes or retired as resolving
artifacts; a re-measure under the complete oracle at any earlier named
point (a `--changed` window covering `internal/author`) closes it
sooner.
