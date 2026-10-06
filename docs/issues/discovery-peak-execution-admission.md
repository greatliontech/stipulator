# Discovery peaks can prevent package admission after discovery releases memory

Lands: user decision

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
