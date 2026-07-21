# Departed identities' witness variants accumulate without bound

Lands: when the witness store next gains a maintenance surface, or when
a long-lived corpus's store size first becomes a measured cost.

## Observed

Per-identity variant eviction fires only on a new install of the same
identity. A departed test — deleted, renamed, or moved out of policy —
never installs again, so its variants linger for the corpus's lifetime:
disk plus per-file validation cost in every load, never wrong serving.
The per-corpus store similarly persists after a corpus is deleted.

## Measurement (2026-07-21, both live corpora on one workstation)

30 MB total across the store: 17 MB / 947 files (gofresh, ~490
identities with tree-state variants from heavy development), 13 MB /
711 files (stipulator), plus ~30 orphaned 20 KB corpus dirs left by the
since-fixed store-hermeticity leak. Load-time per-file validation cost
is unmeasurable at this size. No mechanism is warranted yet.

## Resolution

A store GC that drops identities absent from the current obligation
universe (or variants beyond an age bound), run as an explicit verb -
never opportunistically: identities absent from THIS tree state may be
live on another branch, and silent eviction would undo the variant
store's branch-alternation serving. Lands only on a measured cost.
