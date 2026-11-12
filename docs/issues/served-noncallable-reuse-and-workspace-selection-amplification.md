# Non-Callable `ClosureStrategy` Breaks `reuseCheckedCapture` & `go.work` `buildSelection` 6x `openingCapture` Amplification

- **Status:** Open
- **Governing contract:** [`docs/specs/evidence.md`](../specs/evidence.md) (`REQ-evidence-resolution-freshness`, `REQ-evidence-resolution-freshness-typed-load`), [`docs/specs/backends/go.md`](../specs/backends/go.md) (`REQ-go-build-selections`, `REQ-go-workspace`, `REQ-go-fuzz-exploration`)
- **Lands:** Chunk 2 in [`docs/plans/plan.md`](../plans/plan.md)

## Problem

1. **`serveSelection` Omits Non-Callable `ClosureStrategy` Symbols from `checkedCaptures`, Causing 100% `reuseCheckedCapture` Misses (`served.go:480-505, 634-637`)**
   - In `serveSelection` ([`internal/backends/golang/served.go`](../../internal/backends/golang/served.go) lines 634–637), non-callable bindings (constants, types, variables, struct fields such as `gofresh.ClosureStrategy` and `gofresh.RecordedBatchError`, where `rec.Fingerprint == (gofresh.Fingerprint{})`) are partitioned into `nonCallables` and omitted from both `s.checkedCaptures[key]` and `s.checkedUnknown[key]`.
   - When `openingCapture` (`line 455`) subsequently calls `s.reuseCheckedCapture(key, s.unserved)` (`lines 480–505`), it loops over `s.unserved`, encounters the first non-callable symbol (`line 501`), finds it absent from both `checked` and `unknown`, and returns `false` — discarding all `1,198` freshly computed callable fingerprints and re-running `engine.Snapshot` + `CaptureBatch` a second time (`32.8s` wasted on `gofresh`).

2. **Under `go.work`, `buildSelection.members(root)` Ignores `sel.moduleRoot` and `openingCapture` Eagerly Evaluates All Corpus Symbols Across Every Selection (`resolutioncontext.go:36-38`, `served.go:453-466`)**
   - For a workspace with root `.` and submodules `stipulate` and `stipulate/structural`, `policyBuildSelections` (`golang.go` lines 533–549) constructs 6 `buildSelection`s (`default`, `module=stipulate`, `module=stipulate/structural`, `tags=race`, `module=stipulate,tags=race`, `module=stipulate/structural,tags=race`).
   - In `buildSelection.members(root)` ([`internal/backends/golang/resolutioncontext.go`](../../internal/backends/golang/resolutioncontext.go) lines 36–38), whenever `hasWork && workspaceMode != OFF`, `members` returns `workspaceMembers(root)` (`[".", "stipulate", "stipulate/structural"]`), completely ignoring `sel.moduleRoot`.
   - `declarationPackages` (`packageinventory.go` lines 56–84) therefore assigns all `1,264` corpus symbols to every selection's inventory, and `openingCapture` (`served.go` lines 453–466) eagerly runs `captureUnder` across all 6 selections (`12` `observeView` passes x `1,264` symbols = `15,168` subject evaluations, `133.8s`) *before* typed resolution runs — even though `"default"` already covers all three workspace members and resolves `100%` (`1,268/1,268`) of the corpus symbols (`0` symbols resolved by the remaining 5 selections).

3. **Solo Execution Overhead for Deterministic `Fuzz*` Seed-Replay Targets (`selection.go`)**
   - `selection.go` (`cause: "mixed_fuzz"`) partitions every `Fuzz*` target (`20` in `gofresh`) out of batch execution into separate solo `package.test` invocations even during deterministic committed-seed replay (when `-test.fuzz` exploration is not active, `REQ-go-fuzz-exploration`), adding `20` solo process startups (`~45s` on cold race runs).

## Resolution Scope

1. Track non-callable symbols in `s.checkedUnknown[key]` (or an explicit `s.checkedNonCallables[key]` set) during `serveSelection` so `reuseCheckedCapture` reuses all verified callable captures when `s.unserved` contains non-callable declarations.
2. Deduplicate equivalent workspace `buildSelection` member sets when `go.work` is active (or scope submodule selections appropriately under `REQ-go-workspace` / `REQ-go-build-selections`) and capture secondary selections lazily only for symbols that remain unresolved after primary selection typed loading.
3. Verify whether Go's compiled test binary executes `Fuzz*` seed-corpus checks cleanly alongside `Test*` functions in a single `-test.run` invocation when `-test.fuzz` is unset; if Go's harness supports mixed seed-replay without altering semantics, avoid `mixed_fuzz` solo splitting, or document the Go `testing` harness constraint if solo partitioning is required.
