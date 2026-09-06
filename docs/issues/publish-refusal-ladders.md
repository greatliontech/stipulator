# Witness publication refusal ladder written twice

The selective runner's `publishExecuted` with `grantingRun`
(`internal/backends/golang/witnessrun.go`) and the full-form recorder's
`publishGroup` (`internal/backends/golang/derive.go`) each walk the
same per-subject refusal ladder — the random-seeded refusal, the
missing pre-execution fingerprint, the granting-process eligibility,
the outcome-word fold with its contradiction refusal — in two bodies
with two reason vocabularies ("no healthy process granted the outcome"
beside "no healthy outcome for the subject"). A subject can therefore
be served from one form and refused from the other for the same
evidence. One per-subject publish judgment, fed by each form's
producer lookup and emitting one reason vocabulary, would collapse
them; the serving-integrity rule (a served witness was granted by a
healthy process whose fingerprint was taken before execution,
REQ-evidence-freshness-no-health) is the invariant to preserve.

Lands: with the next change set touching witness publication in
either form (`publishExecuted`, `grantingRun`, or `publishGroup`).
