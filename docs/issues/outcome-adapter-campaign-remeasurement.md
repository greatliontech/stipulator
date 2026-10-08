# Outcome adapter campaign remeasurement

The installed mutation runner predates the shared execution-outcome protocol and
the nodwarf5 source audit. Its adapter campaign produces machine-local findings
with unverifiable freshness: the old selection audit refuses the experiment,
the backend test harness exposes mutated package state, and its startup cleanup
is outside the supported observation model. These findings cannot establish a
reusable assertion audit.

Remeasure `.gomutant/outcome-targets.json` with the migrated runner. The named
preparation witness includes the nil-leg, whole-package selection and cancelled
preparation cases; `.gomutant/ephemeral-attestations.json` records the redundant
cardinality and zero-on-error equivalences. Preserve the distinct evidence:
executed kill probes are execution evidence, equivalences are reasoned judgments,
and a stale or unverifiable campaign row is neither a passing audit nor an
unjudged equivalence. Any remaining fixture limitation needs an honest refusal,
not an added purity assertion to manufacture oracle support.

Lands: pew performance-evidence plan chunk 6, after gomutant's producer and dependency migration.
