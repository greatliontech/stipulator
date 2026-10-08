# Completed-package peak stops cold witness admission

Lands: user decision

Field report from bldc on 2026-10-09. The production authoring-writer change set
is recoverable at bldc commit `14e743d` (parent `e095733`). This invocation
preceded that landing's later test-only corrections to the direct-write detector;
the named commit identifies the production delta, not an assertion that its
final test-source bytes exactly match the failed invocation. The full uncached
suite, root guards and 217 web tests passed before the reported invocation.
`mlock run task check` then completed discovery but refused much of witness
execution on the memory admission term. This is incomplete verification, not a
failing test assertion or a passing check.

The invocation took 24m34.5s: compile 49.9s, discovery 23m2.2s, execution 40.3s,
verification 2s. The discovery-exit diagnostic reported a 7.8 GiB peak. After
several packages completed, remaining packages reported:

> memory: one package estimated at 7.8 GiB — package
> github.com/greatliontech/bldc/internal/compile/energy's completed process
> 4122341's peak; the host cannot hold one more package process beside the pass:
> available 7.7 GiB, 0 package(s) running reserving 0 B, the pass's resident
> 533 MiB (this phase's peak 533 MiB)

The verdict was explicitly `check: fail`. An unchanged-tree retry passed in
11m32.8s: compile 8.7s, discovery 54.9s, execution 10m27.2s, verification 2s,
with a 612 MiB peak at discovery exit. No memory guard, policy, property depth
or execution deadline was weakened. Host conditions and record/cache reuse
were not isolated, so this is not a controlled performance comparison.

The installed binary inspected afterward was v0.73.6 at `c01a298`, built with
Go 1.27.1 and gofresh v0.112.1. The required upstream pull reached `8a79da1`;
the observation was not rerun under a newly built upstream binary.

The earlier cold-package-estimate report was closed by `d6aee66`, following
`66a118f` and `40f85d9`. Its origin-diagnostic request is satisfied here: the
message names the package and completed process. This report does not reopen
that resolved request or assert that discovery's peak directly priced the run.

Investigate what the named completed-process peak measures, including whether
it reflects the package's own work, inherited high-water state or another
measurement boundary. Then determine whether applying that peak to remaining
packages is required protection or an unnecessarily conservative estimate.
The matching numerical discovery peak alone does not establish causality.
Preserve fail-closed admission and an explicit incomplete verdict while the
tool owner sequences this investigation.
