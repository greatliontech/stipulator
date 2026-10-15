# The three reviewed row sets spell one pattern three times

A capture group's policy carries three reviewed row sets — observation
exclusions, dynamic-state vouches, and scratch namespaces — and each
spells the same pattern in its own words: a record-only validation
(`validateExcludedPathForm`, `vouchIdentity`,
`validateScratchNamespaces`), a canonical form on the normalized
invocation (`canonicalExclusions`, the sorted vouch identities,
`canonicalNamespaces`), a group-key segment, a copy on the witness
record (`ObservationExclusions`, the used vouches riding the fingerprint,
`ObservationNamespaces`), and the set's own serving rule. Exclusions and
namespaces require recorded-set containment (`exclusionsStillAsserted`,
`namespacesStillDeclared`). Vouches instead feed the current native judgment;
only acceptances actually used by the verdict ride its evidence. Withdrawing
an unused policy vouch does not itself remove recorded support. The namespace
set's serving gate was an omission its review caught — the pattern
had no one place to be missed from. One reviewed-row-set shape (a
validated, canonical, keyed, recorded, gated set over an element type)
would make the next set structurally complete. Preserve the record's pair
type (`witnesscache.ScratchNamespace`) and its wire contract until native
adoption deliberately replaces it. `namespaceCoversPath` is a copy of gofresh's unexported matcher
with no drift witness beyond stipulator's own rows — an exported
matcher is relayed to gofresh. Invariants preserved:
every row reviewed at acceptance; canonical order and deduplication;
the key partition without re-addressing; recorded-set containment for
exclusions/namespaces and current native judgment with recorded used
acceptances for vouches.

Lands: tooling-redesign 5.2d

The remaining consolidation belongs with native package-producing adoption:
carry each declaration's distinct trust meaning through complete process/build
descriptions, keys, records and revoked-declaration checks. Existing row-set
validation and serving gates are protections to preserve, not missing work.
Use a common lifecycle only where it preserves those meanings; do not turn
selection overrides into union or broaden vouch authority. Complete selections
and module-mode/cross-view certificates are already implemented.

Secondary constituent: 5.2e supplies withdrawal, restart and historical
non-upgrade cases. Namespace matching remains a shared-library boundary to
reconcile during adoption, not permission to copy another matcher.
