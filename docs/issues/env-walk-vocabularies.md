# Environment walks: two vocabularies over one `KEY=value` list

The runner-environment report walks the process environment through
`envIndex`/`sortedKeys` (`internal/backends/golang/envreport.go`) —
an indexed map view — while the invocation normalizer edits the same
list through `lookupEnv`/`setEnv`/`dropEnv`
(`internal/backends/golang/normalize.go`) — a positional list view.
`internal/cmd/witness.go` carries a second `sortedKeys`. Two views of
one value means a key-equality rule (case, the `=`-in-value edge, a
duplicated key) is decided in two places. One environment type —
list-backed, with the index derived on demand — consumed by both the
report and the normalizer would collapse them; the reported
environment equalling the executed one (REQ-evidence-flip-environment) is
the invariant to preserve.

Lands: with the next change set that ADDS a key-equality decision to
the runner environment (a case rule, the `=`-in-value edge, a duplicated
key) in the report or the normalizer's environment edits — the
telemetry-home entry went through the existing list view alone and
decided no equality rule, so it did not fire this.
