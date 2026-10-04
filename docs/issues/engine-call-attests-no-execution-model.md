# The witness engine call attests no execution model — every own-code discharge channel is dead under stipulator

Field report from tugboat (filed uncommitted in this tree, the
standing channel), 2026-09-29, stipulator e9254e7 over gofresh
v0.107.0-era.

gofresh's own-code discharges are two-leg: the author's half is a
source directive (`//gofresh:single-subject` on a declaration) and
the caller's half is an execution-model attestation on the engine
call — `WithSingleSubjectExecution` (each measured subject owns its
process) or `WithPackageProcessExecution` (every process running a
measured subject is the subject package's own test binary). Either
leg alone confers nothing (gofresh closure.md, the shared-dynamic-
state section). pew attests single-subject (cmd/pew/status.go:203);
gomutant attests package-process (freshness.go:159). stipulator's
engine calls attest neither: `grep -rn 'WithSingleSubjectExecution\|
WithPackageProcessExecution'` over this tree finds nothing outside
the evidence spec's record-key list, so the reachability judgment
never applies to a stipulator witness — an unattested consumer "may
run the subject under any root set at all".

What that costs a consumer: tugboat put `//gofresh:single-subject`
on its two sync.Pools (wal.coldBufPool, transport.framePools) after
`stipulator check` named them as the dischargeable class; the next
check still lists them — 45 and 450 witnesses — because no attestation
backs the directive. pew's runs over the same declarations discharge
them.

stipulator's execution model is the package-process one: each
invocation runs a package's subjects in the package's own test
binary (evidence.md's package-process wording; the check face's
"N subjects in M packages"). Attesting it is a true statement about
what the engine spawns, and it is exactly what gomutant attests for
the same shape. (Under package-process the two pools themselves
would still not discharge — harness roots mutate them — but every
init-determined culprit across the corpus would, and the
single-subject directive would be honestly refused rather than
silently ignored; the honest next remedy for the pools is
tugboat's, filed there.)

Expected: the engine call carries `WithPackageProcessExecution` (or
the single-subject one where a policy declares per-witness
processes), and a `//gofresh:single-subject` directive met under an
execution model that does not back it is named on the uncacheable
face as unbacked, never silently ignored.

Lands: cross-tool train chunk 309 (chartered at 307.1, 2026-10-04;
directly after 307 in stipulator's order).
