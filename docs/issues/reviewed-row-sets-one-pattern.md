# The three reviewed row sets spell one pattern three times

A capture group's policy carries three reviewed row sets — observation
exclusions, dynamic-state vouches, and scratch namespaces — and each
spells the same pattern in its own words: a record-only validation
(`validateExcludedPathForm`, `vouchIdentity`,
`validateScratchNamespaces`), a canonical form on the normalized
invocation (`canonicalExclusions`, the sorted vouch identities,
`canonicalNamespaces`), a group-key segment, a copy on the witness
record (`ObservationExclusions`, the vouches riding the fingerprint,
`ObservationNamespaces`), and a still-declared serving gate
(`exclusionsStillAsserted`, `namespacesStillDeclared`). The namespace
set's serving gate was an omission its review caught — the pattern
had no one place to be missed from. One reviewed-row-set shape (a
validated, canonical, keyed, recorded, gated set over an element type)
would make the next set structurally complete. The record's own pair
type (`witnesscache.ScratchNamespace`) stays: the record owns its wire
tags. `namespaceCoversPath` is a copy of gofresh's unexported matcher
with no drift witness beyond stipulator's own rows — an exported
matcher is relayed to gofresh. Invariants preserved:
every row reviewed at acceptance; canonical order and deduplication;
the key partition without re-addressing; serving only while each row
is still declared.

Lands: cross-tool train chunk 226 (policy row-set validation is 226's subsystem; moved from
249 at audit 263).
