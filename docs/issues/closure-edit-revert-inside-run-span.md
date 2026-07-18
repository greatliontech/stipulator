# A mid-run source edit and exact revert escapes closure revalidation

Lands: when witness fingerprints gain pre-run-evaluation support able to
bind closure content to the compile that consumed it, or when witness
records are next redesigned.

## Context

Witness closure fingerprints are captured before execution and served
records are revalidated after it, both over content alone. A single
mid-run source edit therefore reads stale — the safe direction. An edit
and its exact revert both landing inside one package's
capture-compile-run span restore the recorded hash over outcomes a
transiently-edited binary produced: the pre-run capture and the post-run
revalidation both see the reverted tree. Residual to any
capture-and-revalidate scheme whose fingerprint is content-only.

The runtime-input half of this family is narrowed, not closed: completed
observations seal on pre-spawn observation brackets whose fingerprints
cover content and metadata together, so a mid-span mutation persisting
across the run-to-ingest span — including a restore that does not
reproduce the recorded metadata — moves the bracket toward re-execution
(REQ-evidence-witness-freshness). A content-and-metadata-exact restore
completed within the span remains the residual gofresh's
observation-coherence contract (REQ-inputs-observation-coherence)
declares unprovable — the same capture-and-revalidate residue — while
closure fingerprints hash content alone, so on the closure side an
ordinary content-exact revert already suffices.
