# Findings store reads historical under the installed gomutant (fleet sweep 2026-10-26)

The weekly fleet sweep's store inspection reads `.gomutant/` here
(28K; 2 ephemeral equivalence attestations beside it) as `oracle
execution policy "": oracle execution policy is missing or
unsupported; re-measure with the complete oracle; counts are
historical` — 0 repo-committable, 3 machine-local. The installed
gomutant stamps every record with the oracle execution policy its
measurement ran under and lends no authority to a record lacking one;
the three records predate the policy, so they are history until
re-measured under the complete oracle or pruned as resolved. First
read this way at the 2026-10-12 sweep, the same count every week
since; tracked in this index until the index's close-out, and
persisting.

Lands: 3.1 (the canonical plan, gofresh `docs/plans/plan.md`) —
stipulator's first executable chunk; its close-out mutation campaign
runs on the installed gomutant and re-measures the records under the
complete oracle by construction (`prune` retires the resolved-dead
ones) — one trigger, no second one minted. Closing observation: the
sweep's stipulator store line naming a policy, or showing no records.
