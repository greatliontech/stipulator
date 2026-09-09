# Two implementations of "publish at the last covering invocation's completion"

The witness-only selective runner (`internal/backends/golang/witnessrun.go`:
`pendingInvs`, `invGroups`, `onInvocationDone`, `installNow`) and the
health-judged recorder (`derive.go`: `completed`, `covered`,
`invocationCompleted`, `publishRemaining`) each encode the one rule
REQ-policy-cancellation states — a witness group installs the moment
every invocation covering one of its packages has completed — with
different bookkeeping: a pending-set decrement over `witnessGroup`
against a completed-set predicate over `captureGroup`, only the latter
skipping ambiguous packages. One concept, two mechanisms that can
drift.

Resolution: one completion tracker over capture groups, owned by the
recorder, that both forms drive — the selective runner's groups are
capture groups with a served/stale split — and one install-and-note
step; the selective form's pending-set bookkeeping deleted.

Lands: cross-tool train chunk 223