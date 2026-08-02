# 24 observation passes are 86% of the warm check floor

Measured (gofresh repo corpus, quiescent tree, 2026-08-02, back-to-back
runs of a v0.43.8-linked build): a fully-warm `stipulator check` — zero
stale tests, everything served — spends 94.2s of its 109.2s wall in 24
gofresh `observe` passes (~3.9s each: the typed load of every
mutable-local module package, the closure batch, graph metadata), plus
8.6s of `runtime` events. One quiescent tree needs nowhere near 24
observation passes: the check pipeline rebuilds per-group views across
its stages (witness preparation, publication capture, retry) and adds a
per-solo-runnable-package view for the observation-proof leg — the same
caller-side view-cardinality family gomutant measured at 273 passes
(observation-pass-cardinality there), at check scale.

Context that sized it: the same warm check on the PRE-memo engine
(gofresh v0.42.1, the installed binary before 2026-08-02) took 1,048s —
the persistent memos delivered by the version bump are a 9.6× warm
floor cut on their own; this item is the dominant remainder.

Sketch: one view per (engine-config, tree-generation) shared across the
check's stages and groups — REQ-closure-batch-equivalence licenses the
sharing with per-subject evidence unchanged — and the solo-package
proof legs batched into it.

Lands: alongside the gomutant observation-pass-cardinality item — one
fix family, both callers.
