# Findings store reads historical under the oracle-execution-policy judgment (fleet sweep 2026-10-12)

The weekly fleet sweep's store inspection reports this repo's
`.gomutant` store (28K: three machine-local records —
`internal/author.Gap` with 24 open survivors among them — and two
ephemeral equivalence attestations beside them) as

    oracle execution policy "": oracle execution policy is missing or
    unsupported; re-measure with the complete oracle; counts are
    historical — 0 repo-committable, 3 machine-local

The repaired Gomutant record policy stamps every new measurement with the oracle
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

An empty committed `.gomutant/findings.json` does not erase the machine-local
overlay that the reported inspection read. The record source is the merged
committed/local store, not the committed document's row count alone. The
same sweep also reported historical stores in tugboat, pando and protodb;
their scope and dispositions are separate from this repository's records.

Lands: tooling-redesign 9.3

Inventory the merged layers under identified tool and store provenance,
then remeasure the named authorized complete-oracle scope or explicitly
retire its historical records. Neither this filing nor an empty committed
document authorizes a campaign or claims that the reported overlay is gone.
Secondary constituents: 9.1 reconciles earlier scopes; 9.2 preserves history
without upgrading missing support. Closure needs the actual overlay's
disposition and survivor account, not a version-only reinterpretation.
