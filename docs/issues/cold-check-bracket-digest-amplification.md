# Cold check reads terabytes digesting a fixed bracket tree

Every executed witness digests its declared bracket trees at both
observation endpoints, and nothing memoizes those digests across
witnesses within one run: N executed witnesses over an unchanged
bracket tree of size S cost ~2·N·S of hashing reads.

Measured: a cold check on a 450 MB corpus whose `bracket_paths`
include a ~400 MB docs tree sustained ~300-800 MB/s of syscall reads
for over an hour — more than 3 TB read to digest a tree that never
changed during the run (observed live via `/proc/<pid>/io` and the
open-fd walk through the bracketed docs tree).

Scope of the cost: warm runs mostly avoid it (served witnesses do not
execute), so the warm floor is unaffected — but a cold store, an
evicted store, and every permanently-uncacheable residual witness pay
it on each run. It compounds the run-end publication defect
(witness-evidence-published-only-at-run-end.md): a mid-run death
re-pays the full digest bill.

Mechanism home is cross-cutting: the endpoint digests are gofresh's
observation-bracket evaluation; the per-run sharing opportunity (one
digest of an unchanged tree serving every witness in the run) could
live as a gofresh per-process memo keyed by stat identity, or as
run-scoped reuse in this repo's witness runner. Soundness constraint
either way: a bracket endpoint must still detect mid-run mutation of
bracketed trees — a shared digest may only serve while the tree's
stat identity provably held.

Lands: cross-tool train chunk 19.
