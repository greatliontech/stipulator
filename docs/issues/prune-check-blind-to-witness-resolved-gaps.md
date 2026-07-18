# The unwitnessed prune lint cannot detect witness-resolved gaps

Lands: when the unified check evaluates gap resolution inside its witnessed
single pass, or when the prune lint gains a witnessed evaluation mode.

## Observed

`prune --check --no-test` evaluates coverage unwitnessed, which suppresses
all witness and proof evidence, so no requirement of a kind that resolves
through an executed witness can reach covered in that evaluation and its
resolved gap is structurally undetectable by the lint. The CI step's comment
assumes the preceding gate's witnessing carries over; it does not — the lint
re-evaluates from scratch. A gap whose requirement is witness-covered
therefore lingers until an interactive gate reports it resolved.

## Resolution

Evaluate gap resolution against witnessed coverage — either inside the
unified check's single evaluation pass or by letting the lint consume the
witness evidence the same run produced.
