# Eliminate Redundant `gofresh.Session` / `go list` Passes and Split Oversized `internal/backends/golang` Files

**Lands:** Chunk 6 in `gofresh/docs/plans/plan.md` (`Redesign Snapshot / Sibling Observability Sharing & stipulator Solo Scoping`).

## Context & Concrete Findings

### 1. Double Full-Repository `gofresh.Session` + `Snapshot` Load in `captureNativeGeneration` (`internal/backends/golang/native.go:98-221`)

- `captureNativeGeneration` constructs `session` (`Execution: gofresh.PackageExecution`) over all runnable subjects in `g.packages`, calls `session.Snapshot(ctx, subjects, gofresh.CodeResult)` (`native.go:121`), `snapshot.CaptureBatch(ctx)` (`native.go:129`), `snapshot.BindRuntime(ctx, probeRuntime)` (`native.go:137`), and `binding.CheckBatchWithLedgers(ctx, batchCands, nil)` (`native.go:151`) to find subjects with `v.Verdict.Ground.Cause.Kind == gofresh.SharedDynamicStateCause` (`native.go:158`).
- It then constructs a **second** `gofresh.Session` (`soloSession`, `Execution: gofresh.SingleSubjectExecution`) over the **exact same `buildDesc`** (`native.go:171`) and calls `soloSession.Snapshot(ctx, candidates, gofresh.CodeResult)` (`native.go:178`), `candSnapshot.CaptureBatch(ctx)` (`native.go:185`), `candSnapshot.BindRuntime(ctx, probeRuntime)` (`native.go:192`), and `soloBinding.CheckBatchWithLedgers(ctx, soloCands, nil)` (`native.go:203`).
- Because `soloSession` is a separate `gofresh.Session`, `soloSession.Snapshot` reloads `go/packages.Load`, `ssautil.AllPackages`, `prog.Build()`, RTA callgraphs, and closure hashes for the entire repository from scratch (~14.5s on top of 8.0s), even though `BuildDescription` is identical and only the package-peer dynamic-state/carrier discharge differs between `PackageExecution` and `SingleSubjectExecution`.

### 2. Redundant `gofresh.Session.Snapshot` + `captureSourceCertificates` in `Served.openingCapture` (`internal/backends/golang/served.go:275-315, 420-518, 524-643, 738-755`)

1. In `NewServed` (`served.go:223-323`), `serveSelection` (`served.go:524-643`) constructs a `gofresh.Session`, calls `engine.Snapshot` (`served.go:568`), and calls `current, err := view.CaptureBatch(s.ctx)` (`served.go:603`) for all recorded callable symbols.
2. When any recorded symbol is stale, `serveSelection` discards `current[subject]` (`served.go:615-626`) and puts the symbol in `s.unserved` (`served.go:310-314`).
3. Immediately afterward in `Ladder`, `verify.Resolve` triggers `s.ensureBackend()` -> `s.openingCapture()` (`served.go:420-445`), which constructs a **new** `gofresh.Session` (`served.go:460`) and re-runs `engine.Snapshot` + `view.CaptureBatch` (`served.go:479, 508`) and `s.captureSourceCertificates(endpoint, s.unserved)` (`served.go:440`) for those same symbols.
4. Furthermore, in `s.captureUnder` (`served.go:460-475`), `selectionEngine(s.ctx, selected, s.sample)` (`served.go:460`, spawning `go env -json`) runs **before** checking `if len(subjects) == 0 { return nil }` (`served.go:473`).

### 3. Duplicate `declarationPackages` (`go list`) at the Opening of `NewServed` (`internal/backends/golang/served.go:245, 276`, `sourcecertificate.go:357`)

- At the opening of `NewServed`, `s.inventoryPackages(endpoint, symbols)` (`served.go:245`) calls `declarationPackages(s.ctx, view, symbols)` (`packageinventory.go:42`, spawning `go list -e -json=...` per workspace member and selection), and `s.captureSourceCertificates(endpoint, recordedSymbols)` (`served.go:276`) immediately calls `declarationPackages(s.ctx, view, symbols)` again on the exact same opening `endpoint`.

### 4. Unscoped Whole-Group Snapshot in `retryDrifted` (`internal/backends/golang/witnessrun.go:1514`, `retryvalidation.go:238`)

- When a subset of subjects `driftedByGroup[wg]` drifts mid-run, `retryDrifted` (`witnessrun.go:1514`) and `recheckRetryWave` (`retryvalidation.go:238`) snapshot the entire policy group `wg.g` instead of narrowing to the drifted packages/subjects.

### 5. Oversized Multi-Concern Files in `internal/backends/golang` (`execute.go`, `derive.go`, `golang.go`, `witnessrun.go`)

- `execute.go` (`1,895` lines), `derive.go` (`1,868` lines), `golang.go` (`1,770` lines), and `witnessrun.go` (`1,715` lines) mix distinct responsibilities that can be split into focused files within `package golang` (`admission.go`, `teststream.go`, `pgo.go`, `recorder.go`, `seeding.go`, `execmerge.go`) with zero `.stipulator/bindings/` changes.

## Resolution

Land all five improvements in Chunk 6 (`gofresh/docs/plans/plan.md`).
