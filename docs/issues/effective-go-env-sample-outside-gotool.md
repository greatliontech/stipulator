# The normalization's `go env` sample runs beside gotool's snapshot, not through it

The invocation normalizer samples the nine pin-at-load values with its
own `go env` child through the owned command boundary
(REQ-go-owned-processes: the normalization's sample runs inside the
boundary). gofresh's `gotool.TakeEnvSnapshot` — the one environment
snapshot every consumer is meant to read — spawns through a bare
runner with no boundary hook, so adopting it would move the sample out
of the owned process group. The sample joins the snapshot when gofresh's
`Runner` carries the snapshot form (gofresh
docs/issues/gotool-snapshot-lacks-the-boundary-hook.md), at the bump
that consumes that release: `effectiveGoEnv` then reads
`Runner{Prepare: configureCommandCancellation}.TakeEnvSnapshot` and the
nine-line parse goes.

Lands: cross-tool train chunk 272 (the bump behind gofresh 265, which carries the multi-key
read and the self-taking snapshot).
