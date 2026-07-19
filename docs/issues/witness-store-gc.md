# Departed identities' witness variants accumulate without bound

Lands: when the witness store next gains a maintenance surface, or when
a long-lived corpus's store size first becomes a measured cost.

## Observed

Per-identity variant eviction fires only on a new install of the same
identity. A departed test — deleted, renamed, or moved out of policy —
never installs again, so its variants linger for the corpus's lifetime:
disk plus per-file validation cost in every load, never wrong serving.
The per-corpus store similarly persists after a corpus is deleted.

## Resolution

A store GC that drops identities absent from the current obligation
universe (or variants beyond an age bound), run opportunistically or as
an explicit verb. Cost-only; sizing should come from a real corpus
measurement before any mechanism lands.
