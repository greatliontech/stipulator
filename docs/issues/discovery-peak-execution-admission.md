# Discovery peaks can prevent package admission after discovery releases memory

Lands: cross-tool train chunk 290.2b — the resident fold's admission: the pass's growth term scoped to the execution phase and the package estimate to the registered package trees (derived at 290.2b.1: a lifetime peak and a departing resolver child priced a package that nothing running would take; the 4 GiB-budget refusal below is the same shape — 466 MiB resident, a 7.3 GiB lifetime peak, one package estimated at 7.3 GiB)

Field report from bldc on 2026-10-06, installed stipulator
`v0.71.1-0.20261006111733-c6124bd111f6` (Go build metadata), Linux amd64.
The corpus contained 579 requirements. An uncached direct Go suite passed
before the check. The repository was pulled with `git pull --ff-only` to
`9d08ebf`; that update changes CI and issue records, not the admission code.

Running `mlock run task check` reached Stipulator execution but refused some
package witnesses with this diagnostic:

> available 13.2 GiB, 0 package(s) running reserving 0 B, the pass's resident
> 321 MiB (peak 7.7 GiB), one package estimated at 7.7 GiB

The resulting check was red with degraded witnesses. This is a stated resource
refusal, not a crash or evidence that those tests failed. In
`internal/backends/golang/execute.go`, `admission.estimate` includes the largest
observed descendant peak, and `admission.room` additionally reserves the
parent's growth back to its process peak. Discovery peaks can therefore consume
the budget of execution even after the large discovery allocations are released.

The supported workaround `mlock run env GOMEMLIMIT=2GiB task check` executed
the witnesses without that refusal (reported discovery peak 988 MiB on the
first bounded pass). That pass found only an already-resolved gap record;
after ordinary `stipulator prune`, the bounded canonical check passed. This
comparison establishes a usable heap-budget workaround, not that either peak
can safely be discarded from admission for every workload.

Please evaluate default budgeting and recovery guidance for this shape. The
decision is whether to retain the conservative cross-phase reservation with
actionable heap-budget advice, or to derive phase/process-specific estimates
that permit more progress while preserving host protection. No unbounded spawn
or bypass of the memory guard is requested. Related context is
`resolver-child-resident-set.md`; this observation concerns admission after the
release, not an assertion that the released child remains resident.

Further bldc observation on 2026-10-06, after a source/consumer change invalidated
many closures: a gap-pruning run with `GOMEMLIMIT=2GiB` was cancelled at its
30-minute caller deadline while still in discovery (reported peak 7.2 GiB;
no changes retained). A four-requirement scoped check with `GOMEMLIMIT=4GiB`
completed in 33m46.8s: discovery 30m11.1s, execution 3m18.6s, peak 7.4 GiB.
This is an observed duration and resident peak, not proof of their internal cause.

A later full check with the 4 GiB setting refused most witness packages after
discovery: 10.1 GiB available, 466 MiB parent resident with a 7.3 GiB peak, and
one package estimated at 7.3 GiB. The failed pass published 1,422 resolution
records. The unchanged-tree warm retry passed in 11m40s, with discovery 1m0.2s
and a 1.0 GiB peak. Explicit heap budgeting is therefore not by itself a reliable
remedy for every cold pass; warm discovery had materially lower measured cost in
this sequence. This does not isolate record reuse from other host conditions.
Neither the timeout nor the admission-refused run is a pass.
