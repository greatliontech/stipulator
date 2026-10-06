# An aborted verification pass publishes resolution records nobody accounts for

Found at chunk 307.B2d's review (2026-10-05). The shared verification
pass (verifyrun.Run and Scoped) closes its serving backends through
one closer: on the success path the report carries the account the
close returns; on an error path — the witness run failing, the caller
cancelling — the deferred close still runs, the served backend still
publishes a resolution record for every symbol it resolved typed this
run, and the account is lost with the result. check.Run differs: its
result survives the error path, so its account does. Two questions:
whether a pass that did not complete should publish at all (the
records are the next run's served set, derived from a tree the pass
saw whole, so they are sound — but the ending says "kept nothing" of
witness records while resolution records landed), and where the
account rides when there is no result.

Lands: cross-tool train chunk 249 (re-derived at its open: the doc predates 5b03427 — resolution records publish at Quiesce, before execution, not 'at the deferred close'; the clause half — REQ-policy-cancellation's ending naming the resolution records — is 270's; audit 318).