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

Lands: with the next change set touching the runner environment
(the report or the normalizer's environment edits).
