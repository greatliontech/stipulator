# Race witness freshness analyzes the default build

Lands: when stipulator next bumps gofresh to a release carrying
`WithBuildFlags` and build-aware purity scanning.

## Context

`RunTestsFresh` executes selective witnesses under `-race`, which enables the
`race` build tag, but gofresh v0.2.2 accepts that flag only as opaque
`WithBuildInputs` guard evidence. Its closure and purity scanners therefore load
the default source set. A helper selected by `//go:build race` can change while
the analyzed `!race` source and recorded guard remain equal, allowing cached
witness outcomes from different code to be served as fresh.

The gofresh API that separates executable flags from opaque evidence is not in
stipulator's pinned release yet. Until that dependency is bumped and the caller
is migrated, witness freshness under `-race` is not sound; callers requiring the
guarantee use the full witness run.

## Resolution

On the next gofresh bump, construct one `[]string{"-race"}` build selection,
pass it to build-aware purity scanning and `WithBuildFlags`, and remove the old
`WithBuildInputs` call. Add a race-tagged fixture whose selected helper changes,
then prove the old fingerprint becomes stale while the unselected helper cannot
confer purity.
