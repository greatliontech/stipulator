# Bound cache growth across corpora and worktrees

Lands: tooling-redesign 7.9c, after shared memo retention in 7.9a
and gomutant's cleanup surface in 7.9b.

## Evidence and diagnosis

The user's 2026-10-15 disk-pressure report measured `~/.cache/stipulator`
at 3.7 GiB: witness stores 3.1 GiB and resolution stores 602 MiB.
`internal/recordstore` keys stores by canonical corpus directory, so new
worktree paths create independent stores that outlive those worktrees.
Four witness variants per identity and one resolution variant per identity
do not bound the number of identities or corpus roots.

`prune --store` provides useful corpus-local collection, but there is no
global byte bound or abandoned-corpus sweep. Ordinary `prune` disposes
durable gap records and must not be presented as cache cleanup.

## Required outcome

Provide global usage/preview/reclamation and bounded retention for
recomputable witness, ledger, and resolution data, integrating gofresh's
shared memo cleanup without duplicating its mechanism. Cover abandoned
worktrees and obsolete identities, not just variants in the current corpus.

Before implementing automatic cross-identity eviction, reconcile
`docs/specs/evidence.md`'s REQ-evidence-store-gc: explicit corpus-local GC
is currently the only cross-identity eviction, preserving branch-alternation
reuse. Preserve that guarantee or explicitly revise the contract with its
observable recomputation tradeoff; a disk bound does not silently override it.

Keep repository `.stipulator/` configuration, claims, gaps, policies, and
tombstones outside the cleanup surface. Preserve stable sibling lock
inodes and active publication temporaries; coordinate ledger/reference
collection under the existing store lock. Do not remove telemetry-off
configuration while child processes depend on it. Report busy/skipped
stores and reclaimed bytes rather than claiming complete reclamation.

Verify that missing evidence triggers full applicable execution, missing
resolution data triggers analysis, and eviction cannot weaken a verdict.
Exercise concurrent publication/collection, abandoned corpus stores, and
bounded growth across many roots. Document cold execution cost and how
global cleanup differs from `prune --store` and ordinary `prune`.
