# Completed-package peak stops cold witness admission

Lands: a reproduction of the named workload records parent RSS/high-water,
the go test child's wait-status peak, and separate post-exec driver,
compiler/linker and test-binary peaks beside the invocation's origin line.

The estimate reads the go test child's wait-status `Rusage.Maxrss`; its
attribution can include more than the test binary's execution. Measure
`go test -c` under `/usr/bin/time -v` for compilation/linking, then execute
the resulting test binary separately. A low test-binary peak alone does not
identify the build as the cause: the pre-exec inheritance hypothesis below
and the driver's own work are separate possibilities. Choose any estimate
correction only after distinguishing those terms. Real compiler, linker or
test memory still needs protection; a suspected inherited peak is not a
reason to remove the admission bound. Stipulator's own builds measured below
the floor do not establish the peaks of the reported bldc/Weaver workloads.

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

## Weaver observation and admission arithmetic

Weaver reproduced the refusal with the same installed v0.73.6 revision. The cold
pass reported 729 executed witnesses, then degraded remaining packages with:

> memory: one package estimated at 6.0 GiB — package
> github.com/thegrumpylion/weaver/gen/go/weaver/v1's completed process 40325's
> peak; the host cannot hold one more package process beside the pass:
> available 9.1 GiB, 0 package(s) running reserving 0 B, the pass's resident
> 205 MiB (this phase's peak 3.4 GiB)

Discovery had reported a 6.0 GiB peak. The execution admission arithmetic explains
the refusal: the 6.0 GiB package estimate plus the roughly 3.2 GiB parent-growth
allowance exceeds the displayed 9.1 GiB availability. The parent-growth allowance
uses execution's observed peak minus current RSS, not the lifetime discovery peak.
An earlier Weaver pass attributed an estimate of 5.3 GiB to the completed public
invocation-package process, again matching discovery's peak; an unchanged-tree
standard retry completed 1045 tests. These are observations, not controlled peak
comparisons or proof that either package actually required that much memory.

Pinned-source tracing at `c01a2987170cb5610521e7b737e25cc2ffa2c5ce` identifies an
additional measurement-boundary hypothesis. `runPackage` passes
`processPeakBytes(cmd.ProcessState)` to `gate.reaped`; Linux `processPeakBytes`
reads wait-status `Rusage.Maxrss` without a post-exec boundary correction. The
completed maximum then prices subsequent admissions. gofresh v0.112.1 uses Go's
owned runner with a process group; this Go 1.27.1 Linux exec path uses
`CLONE_VFORK | CLONE_VM`.

In upstream Linux v6.12 source, `copy_mm` shares the parent's address space on that
path, and `exec_mmap` carries the old address space's high-water RSS into the child's
signal maximum. This supplies a plausible route for pre-exec parent high-water
state to reach the wait-status estimate after discovery memory has been released.
The report host was Linux 7.2.9-arch1-1; its exact kernel source was not verified.
The source path and matching numbers do not independently establish the contribution
to the two named processes. A reproduction should record parent RSS/high-water,
child wait-status peak, and post-exec driver/compiler/linker/test peaks separately.

Preserve fail-closed admission and incomplete verdicts. A warm retry or a lower
soft Go heap target can change the circumstances but does not settle attribution.
