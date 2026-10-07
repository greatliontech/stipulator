# Cold package admission consumes the invocation deadline

Lands: user decision

Field report from bldc on 2026-10-07. Its full uncached Go suite, root guards
and 217 web tests passed. `mlock run env GOMEMLIMIT=4GiB task check` then
failed after a cold discovery and a 25-minute execution budget. Discovery
took 43m18.9s and reported a 7.3 GiB peak. The memory diagnostic at expiry was:

> available 12.3 GiB, 1 package(s) running reserving 7.3 GiB, the pass's
> resident 675 MiB (this phase's peak 679 MiB), one package estimated at 7.3 GiB

The invocation reported packages in `internal/compile/joints`, `internal/lsp`
and `internal/compile/run` still held by the memory term at expiry. The
`internal/record/ingest` package had started but was interrupted in
`TestImportNestedUpdateDeterminismProperty`; its package output reported
8.265 seconds. The whole check took 1h9m22.7s. This is interrupted evidence,
not a failing test assertion or a passing check.

An unchanged-tree retry passed in 12m8.4s: discovery 1m5s, execution
10m43.4s, with a 1.2 GiB peak at discovery's exit. No guard, policy or timeout
was weakened. The comparison does not isolate record reuse from other host
conditions or identify the origin of the package estimate.

After the run, the installed binary's Go metadata named
`v0.71.2-0.20261007024416-8ef380706888`, the resident-fold release. The
diagnostic also uses that release's phase-scoped peak wording. Repository
inspection after a clean `git pull --ff-only` found the earlier
`discovery-peak-execution-admission` issue resolved by `8ef3807`; this report
does not assert that its lifetime/foreign-peak defect remains. Recovery of
the earlier report: `git log --all -- docs/issues/discovery-peak-execution-admission.md`.

Investigate how the registered-package estimate reached and retained 7.3 GiB
and whether the deadline account provides enough evidence to distinguish a
genuine package need from a conservative estimate. The diagnostic alternatives
are to retain the estimate with actionable budgeting guidance if it reflects
registered work, or to correct its attribution if unrelated work still enters
it. The observed numbers alone do not settle that question. Preserve host
protection and the explicit interrupted verdict in either case.
