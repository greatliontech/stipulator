# Common ledger-store mechanics remain beside the record core

Lands: tooling-redesign 5.6

`internal/witnesscache/witnesscache.go`'s `ledgerCoordinate`, `installLedger`,
`readLedger` and `sweepLedgers` already use complete provenance coordinates,
including binding strategy. Installation and collection share cross-process
exclusion; refused records retain readable ledger references. The ledger is
not named by compartment digest alone. `installLedger` already delegates
atomic writing to `recordstore.WriteAtomic`; the shared `recordstore.Digest`
and `Name`'s encoder-error propagation are also implemented.

The remaining consolidation is common scan, name/content admission and
collection mechanics around the nested ledger store. Reuse a record-kind
mechanism only if it preserves the full coordinate, lazy ledger reads,
binding-evidence validation, referenced-record protection, context checks
and concurrent-install sparing under the same exclusion span. Do not restore
a one-segment compartment-only identity or add redundant atomic writers.

Secondary constituent: 9.2 settles native/envelope compatibility before any
format change; mechanical sharing must not backfill historical evidence.
