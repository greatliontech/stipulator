# Witness evidence is published only at run end

A check's witness run accumulates every produced record in memory and
installs the whole set in one loop after the run completes
(`internal/backends/golang/witnessrun.go`, the publication loop calling
`witnesscache.Install`; the parallel derivation path in `derive.go`
installs the same way). `Install` itself is per-record and atomic
(write-then-rename), so the store supports incremental installs — the
run structure just never uses that until the end.

Failure mode: a run that dies mid-flight — crash, OOM, kill, reboot,
context cancellation — persists nothing. Every completed witness
execution and every bracket digest re-pays in full on the next run.
Measured shape: a cold check on a 450 MB corpus with a ~400 MB
bracketed docs tree ran over an hour of witness execution (~3 TB of
bracket-digest syscall reads) with zero durable bytes the whole time;
death at minute 59 costs the entire hour.

Why it is not a one-line move: publication is end-batched because a
record must survive the run's drop paths — red or aborted processes,
missing observations, failed captures, post-run drift verdicts, and
the degraded path (which publishes nothing) — before it may install.
Incremental publication needs the drop-path decision brought forward
to witness completion (each witness's own bracket endpoints already
close per witness) or a staged install-then-confirm discipline, while
the degraded path must still leave the store untouched.

Not a correctness defect: the store is only ever read through
fingerprint validation, so served evidence cannot be wrong. The loss
is durability of completed work — pure re-execution cost, largest
exactly on the corpora where a check is most expensive.

gomutant's incremental findings persistence (a dying campaign keeps
every committed verdict) is the in-family reference shape.

Lands: user decision.
