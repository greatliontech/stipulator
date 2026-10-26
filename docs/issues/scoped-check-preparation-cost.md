# Scoped-check preparation cost and misleading progress counters

Lands: next Go-backend diagnostic/performance increment. First correct and test
phase/counter attribution; then measure preparation and certificate costs, fix
demonstrated redundant work, and validate cold/warm behavior on generated workloads.
Representative Cerebro acceptance remains a separate downstream confirmation.

## Assignment and independence from Cerebro

This is a report and implementation brief, not a request to clone Cerebro before
starting. The receiving agent can reproduce the diagnostic errors, inspect the
repeated scan, and test fixes entirely in this repository. Synthetic workloads
below supply independent correctness and scaling controls. They are proposed
tests, not a claim that an equivalent synthetic timeout has already been measured.

The original 600-second failure was observed on a large, in-progress Cerebro tree.
Reproducing that exact duration or certifying that workload fixed requires its
identified source/record/cache state, or a later downstream run by its owner.
Neither access to that tree nor a reproduction of its exact wall time is required
to make progress on the concrete findings here. Do not condition the diagnostic
and structural fixes on receiving application source.

## Observed result

On October 26, 2026, Stipulator `v0.75.1` could not finish a single-requirement
check within a 600-second execution budget. It exited through the supervisor with
status 124 during discovery, before any reported witness execution, and produced
zero bytes of JSON verdict. Peak reported resident memory was 5.5 GiB. This is
an observed practical performance failure, not a formal six-hundred-second SLA,
an OOM, an application test failure, or proof of a particular hot function.

Binary identity:

```text
stipulator v0.75.1
revision=59fe114ddaefe59c66e22f7baee103fdc36a325b
modified=false
go=go1.27.2-X:nodwarf5
gofresh=v0.119.1
sha256=140ce38f55c8170b48313c7dc3ffb8d0871d3440223446d7fef7349f16515802
```

Inner command, with the timeout inside the host's shared heavy-work lock:

```sh
timeout --signal=TERM --kill-after=30s 600 \
  stipulator check --ids REQ-corpus-declaration-lowering --json
```

Environment: Linux amd64, selected Go 1.26.4, `GOTOOLCHAIN=local`, `GOPROXY=off`,
`GOSUMDB=off`, `GOMAXPROCS=4`, `GOFLAGS=-p=2`, `GOMEMLIMIT=2GiB`, and populated
existing Go caches. `GOMEMLIMIT` is a soft Go memory target, not a process-tree
RSS cap. Cold versus warm Stipulator cache behavior was not controlled.

Policy: one invocation selecting `./...`, `./machinery/primitives/...`, and
`./sim/...`, with `plain_witness: true`, invocation timeout 7200s, and test argument
`-test.timeout=30m`. This is three workspace modules, not three invocations.
No test execution was reached, so the test timeout did not cause this result.
The root module contains approximately 144 packages; the operation's unserved
symbol frontier was 10,363. These describe scale, not a minimal reproducer.

The application base was `4e41f442c32c0dba7184e27df4d5461bdbccf7b0` with a
reviewed in-progress service implementation and a binding migration. A preceding
attempt stopped immediately on 39 missing-clause binding errors. Those were
corrected before the timed-out attempt, subsequently committed as `95629a09e`.
Neither commit alone denotes the exact observed working tree. The earlier
binding rejection is not this performance failure.

Verbatim decisive log excerpt (intervening per-symbol lines omitted):

```text
phase compile (0s) — at the start: resident 21 MiB (peak 21 MiB), ceiling 2.0 GiB
phase discovery (4m57.4s) — at compile's exit: resident 1.6 GiB (peak 5.5 GiB), ceiling 2.0 GiB
gofresh: cancelled — 0 observability proof slices and 0 scans persisted this operation; a rerun serves them
resolution: 0 served from records, 10363 resolved typed
resolution degraded to typed: publish "default": go list -json -deps -test github.com/candosa/cerebro/machinery/primitives/revision: context canceled
resolution typed: github.com/candosa/cerebro.Engine.Evaluate: source dependencies
...
cancelled in the discovery phase; kept nothing — at the end: resident 1.4 GiB (peak 5.5 GiB), ceiling 2.0 GiB
```

The 600.13s supervisor elapsed time included negligible wrapper overhead. This
report includes the observations needed for triage; no inaccessible scratch path
is needed to interpret it. The originating owner retains complete stderr and
command/environment receipts for later comparison.

## Governing contracts and precise findings

Use [overview](../specs/overview.md), [change](../specs/change.md),
[evidence](../specs/evidence.md), and [Go backend](../specs/backends/go.md) as
authority. Source links below are pinned to the observed revision, not floating
line numbers. Scope controls stale execution while retaining whole-tree fresh
evidence; whole-policy enumeration is **not itself a scope-contract violation**.

### 1. Confirmed diagnostic defect: preparation is attributed to compile

[`Ladder`, lines 31–48](https://github.com/greatliontech/stipulator/blob/59fe114ddaefe59c66e22f7baee103fdc36a325b/internal/check/ladder.go#L31-L48)
calls `OperationSymbols` and constructs serving backends before announcing
`PHASE_DISCOVERY`. `OperationSymbols` discovers policy witnesses, and serving
construction performs package inventory and certificate/cache preparation.
Consequently the observed 4m57.4s compile-labelled interval is not specification
parsing time. Report these operations in the correct phase or explicit subphases,
including cancellation during them. Do not merely rename the final log line.

### 2. Confirmed diagnostic defect: frontier is labelled completed work

[`Served` accounting, lines 558–572](https://github.com/greatliontech/stipulator/blob/59fe114ddaefe59c66e22f7baee103fdc36a325b/internal/backends/golang/served.go#L558-L572)
uses `len(s.unserved)` for the serving-form count labelled `resolved typed`.
That is the unserved operation frontier, **not completed typed resolutions**.
An interruption can therefore report thousands of completed-looking resolutions
that never happened. Distinguish discovered/frontier, served, attempted and
completed work as appropriate to the existing reporting contract; count only
actual events and preserve meaningful partial results on cancellation. Avoid
another redundant accounting authority.

### 3. Confirmed repeated work; performance contribution unmeasured

[`sourceGraphs`, lines 162–201](https://github.com/greatliontech/stipulator/blob/59fe114ddaefe59c66e22f7baee103fdc36a325b/internal/backends/golang/sourcecertificate.go#L162-L201)
builds an import-path index but then scans all returned package rows for every
requested owner to locate its package/test roots:

```text
for each requested package:
    for every returned row:
        select row.ImportPath == pkg, row.ForTest == pkg,
               or row.ImportPath == pkg + ".test"
```

Root selection is O(P × R), for P owners and R rows, before dependency traversal.
Indexing these root relations once can avoid that repeated scan. Preserve exact
root membership, test variants, dependency closure, error attribution and
independent digests. This is structural evidence of redundant scanning, **not
profile evidence that it accounts for the ten-minute timeout**. Measure it before
claiming an end-to-end performance fix.

### 4. Investigation: preparation, freshness and resolver costs

The exact flow is:

1. [`check.Run` setup](https://github.com/greatliontech/stipulator/blob/59fe114ddaefe59c66e22f7baee103fdc36a325b/internal/check/check.go#L77-L180)
   prepares the corpus, policy and scoped execution selection.
2. [`OperationSymbols`](https://github.com/greatliontech/stipulator/blob/59fe114ddaefe59c66e22f7baee103fdc36a325b/internal/backends/golang/served.go#L1154-L1197)
   unions all bound Go symbols with captured-policy witness subjects.
3. [`NewServed`](https://github.com/greatliontech/stipulator/blob/59fe114ddaefe59c66e22f7baee103fdc36a325b/internal/backends/golang/served.go#L147-L231)
   inventories packages, loads records and computes current source certificates
   and selection snapshots. It does not eagerly resolve every frontier symbol.
4. [`verify.Resolve`](https://github.com/greatliontech/stipulator/blob/59fe114ddaefe59c66e22f7baee103fdc36a325b/internal/verify/verify.go#L362-L445)
   requests binding location, witness class, resolution and shape. The resolver
   child's opening capture precedes child startup.
5. [`Close / Quiesce / publishSelection`](https://github.com/greatliontech/stipulator/blob/59fe114ddaefe59c66e22f7baee103fdc36a325b/internal/backends/golang/served.go#L875-L1033)
   performs closing captures before records can be published.

Attribute time and allocations separately to policy witness discovery, Go
listing/export loading, source certificates, native snapshots, child loading,
classification and publication. Explain cache misses by the certificate component
that differs, rather than inferring a cache bug from zero hits.

`source dependencies` means certificate comparison failed; certificate identity
also includes tool/derivation, selection and environment inputs. A tool update
or source edits can legitimately invalidate it. The current excerpt cannot
distinguish these causes. `warmGraphs` already deduplicates owners and attempts
batched listing; do not claim unconditional one-`go list`-per-symbol behavior.

The canceled `go list .../revision` is from **closing publication**. It does not
identify the original bottleneck. Refusing to publish without a successful
closing observation is sound. `kept nothing` does not prove that safe checkpoints
were thrown away, nor describe every effect on Go's build cache.

## Self-contained implementation and acceptance brief

Use the existing test fixtures, Go backend test helpers and benchmark runner.
No Cerebro law, sources, private data or custom application evaluator is needed.
Do not introduce hard wall-time assertions into correctness tests.

1. **Phase attribution:** inject/observe the operation-symbol and backend-creation
   steps through the existing ladder seams. Verify phase ordering before either
   expensive operation, including cancellation in each. Confirm genuine corpus
   compilation still receives its own attribution.
2. **Counter semantics:** construct a serving operation with N unserved symbols,
   resolve only k where k < N, then interrupt/close. Assert that completed count
   denotes actual completed resolution, not N. Cover zero completed, cache hits,
   typed failures and repeated requests for the same symbol. Check CLI/MCP/report
   consumers together where the existing shared account feeds them.
3. **Root-index equivalence:** use generated `sourcePackageRow` streams with
   ordinary package roots, internal/external test variants, test binaries and
   shared dependencies. Compare exact per-owner results with a small independent
   reference selector implementing the three predicates above. Vary order,
   overlapping roots, `ForTest`, missing dependencies, malformed/duplicate package
   identities, per-main PGO variants and cancellation. Preserve existing batching
   fallback and failure behavior. Count root-selection visits or measure isolated
   scaling; total closure work need not be linear in P + R.
4. **Integrated generated workload:** build a temporary three-module workspace
   through existing helpers, with many tiny packages, a shared dependency fan-out,
   generated example/property witnesses and legitimate clause-scoped bindings.
   Use finite scales (for example 16/64/144 owners and increasing witness counts)
   to separate package, witness and shared-closure costs. One requirement selects
   a small witness subset; the accepted policy still covers all modules. Small
   tests assert semantics; larger performance samples belong in benchmarks.
5. **Cold/warm/movement matrix:** run first capture, unchanged warm reuse, one leaf
   source edit, a shared-dependency edit, a record-only change and cancellation.
   Record actual served/resolved/executed counts, subphase time, allocation/RSS
   attribution and cache persistence. Fresh examples should reuse where permitted;
   random-seeded witnesses retain their required execution behavior. Distinguish
   supported reuse from legitimate refusal; never force freshness.
6. **Accept only measured claims:** report which diagnostics and redundant work
   were corrected, plus synthetic measurements and limits. A synthetic pass is
   not Cerebro acceptance. Send the resulting revision and evidence to the
   downstream owner for one bounded representative rerun, then separately consider
   full-gate restoration. No unchanged repeated ten-minute sweeps are needed here.

All heavy checks follow the host's locking rules; benchmarks require the existing
exclusive benchmark arrangement. Preserve work limits, cancellation, fresh/typed
fallback, selected build environment, source-certificate semantics and opening/
closing observations. Do not narrow the application's policy, renew consent, raise
its test timeout or bypass closing capture to obtain a green or faster result.

## Completion boundaries

Diagnostic correctness and the root-selection equivalence/scaling work can be
completed without Cerebro. Precise attribution of the original timeout and a
claim that its end-to-end check now finishes require representative downstream
confirmation. If synthetic costs do not explain the original scale, report that
limit and request the missing measurements rather than assuming the issue fixed.

The prior `cerebro-scoped-discovery-cost.md` issue was removed in commit
`b995160b6033a37762cb403fd26d35e8eb0a4f19`; its historical observations are not
the evidence for this newer run. This report remains open until the concrete
diagnostic findings have been addressed and the performance question has a
measured disposition, with downstream confirmation explicitly accounted for.
