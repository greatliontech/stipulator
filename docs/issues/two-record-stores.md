# The witness cache and the resolution cache are one store shape written twice

`internal/witnesscache` and `internal/resolutioncache` each implement a
corpus-keyed store under the user cache directory — `StoreDir`,
`Install`, `Load`, `GC`, a validity filter over the record's
fingerprint, the identity-plus-fingerprint file naming — and the spec
states the naming discipline twice (REQ-evidence-witness-cache-format,
REQ-evidence-resolution-cache-format). The record kinds differ (a
witness record carries outcomes, registrations, an observation, and
names its compartment's ledger in the witness store's ledger files; a
resolution record carries a resolution's fields), the store does not;
the atomic file writer is already one (`witnesscache.WriteAtomic`), the
rest is still two. One store parameterized by record kind — one directory
layout, one supersede scan, one GC walk, one validity core with a
per-kind fingerprint admission — would delete the second
implementation and let the two format clauses share their naming
sentence.

The served backend (`golang.Served`) is likewise a caching decorator
over the owned child (`golang.Owned`) re-stating each verification role;
folding the two into one backend whose typed path is the degenerate
served path is the same collapse one level up.

Lands: user decision — the ledger files were the third on-disk kind, and
the shared writer was the fold that fit the change set that made them;
the store unification is a refactor of its own.
