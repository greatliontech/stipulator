# Chunk 2 Working Notes — Unified Narrowing Capture, Non-Callable `reuseCheckedCapture` Retention, Workspace Selection Deduplication & `Fuzz*` Batching

## Governing Contracts & Scope

- **Specs:**
  - `docs/specs/evidence.md`:
    - `REQ-evidence-resolution-freshness` (complete cross-view source certificates and per-view source-closure fingerprints)
    - `REQ-evidence-resolution-freshness-typed-load` (opening capture precedes typed load; non-callable declarations narrowed as unknown subjects at both captures; straddle check)
    - `REQ-evidence-resolution-freshness-quiesce` (`Quiesce` hydrates package, class, and `NeverServe` refusal once before `publishSelection`)
    - `REQ-evidence-resolution-cache-format` (5-part selection key encoding; distinct views preserved for explicit `workspace_mode`, independent `module_root`s, tags, toolchains, and module modes)
  - `docs/specs/backends/go.md`:
    - `REQ-go-build-selections` (declaration-precedence order, workspace-on vs. workspace-off views)
    - `REQ-go-workspace` (`go.work` member validation and independent nested module roots)
    - `REQ-go-fuzz-exploration` (deterministic committed-seed replay in ordinary runs)
- **Retires Issue:** `docs/issues/served-noncallable-reuse-and-workspace-selection-amplification.md`

## Root-Cause & Soundness Analysis

1. **Unified `narrowAndCapture`, `publishSelection` Hydration Dedup & Non-Callable `reuseCheckedCapture` (`internal/backends/golang/served.go`)**:
   - Extract `narrowAndCapture(ctx, engine, subjects, onGone)` shared by `captureUnder` and `serveSelection`, eliminating the duplicated 35-line `engine.Snapshot` + `UnknownSubjectsError` narrowing + `CaptureBatch` loop.
   - In `serveSelection`, evaluate `nonCallables` (`rec.Fingerprint == (gofresh.Fingerprint{})`) against `s.checkingCertificates` first:
     - Unchanged non-callables (`!sourceCertificateMoved`) immediately enter `s.served[rec.Symbol]` and never appear in `s.unserved`.
     - Stale non-callables (whose certificate moved or is missing, so they will enter `s.unserved`) join the `narrowAndCapture` subject probe (`probeSubjects`, guarded from the callable comparison loop via `bySubject`). In the normal case where they remain non-callable types/consts/vars/fields, `gofresh` narrows them via `UnknownSubjectsError` into `allGone` (`s.checkedUnknown[key]`); if a declaration refactored from a non-callable `type`/`var` into a callable `func`, `CaptureBatch` records its new callable `Fingerprint` in `s.checkedCaptures[key]`. Always populate `s.checkedCaptures[key]` and `s.checkedUnknown[key]` after a non-degraded `narrowAndCapture` even when `len(subjects) == 0` so `reuseCheckedCapture` hits on all-stale or non-callable-including unserved sets.
   - Remove the redundant `unclassed`/`unrefused` hydration block at the top of `publishSelection` (`Quiesce` already hydrates `a.pkg`, `a.classed`, and `s.refusals` across all `s.answers` in a single batch immediately prior to `publishSelection`, and `publishSelection` has no other callers). Initialize `s.refusals` in `newServed`.

2. **Unified `resolveWorkspaceMode`, `go.work` Member Selection Canonicalization & Precedence-Filtered `openingCapture` (`workspace.go`, `resolutioncontext.go`, `normalize.go`, `golang.go`, `served.go`)**:
   - Consolidate the triplicated `os.Stat(filepath.Join(dir, "go.work"))` + `GoWorkspaceMode` switch across `normalize.go`, `resolutioncontext.go`, and `golang.go` onto `resolveWorkspaceMode(dir, mode) (workspaceOn bool, workPath string, err error)` in `workspace.go` (preserving non-`ErrNotExist` stat errors everywhere).
   - In `policyBuildSelections` (`golang.go`), when `workspaceOn` is true (`go.work` exists) and an invocation leaves `workspace_mode` `UNSPECIFIED` while naming a `module_root` that is already a declared member of `go.work`, Go's active workspace (`GOWORK=<root>/go.work`) loads the exact same workspace member set, module graph, environment, and flags as `module_root: ""`. Canonicalizing `sel.moduleRoot = ""` for implicit workspace-member selections collapses `stipulator`'s 6 redundant views (`default`, `stipulate`, `stipulate/structural`, `race`, `race+stipulate`, `race+stipulate/structural`) to the 2 genuinely distinct views (`default`, `race`), while keeping explicit `workspace_mode: WORKSPACE`, `workspace_mode: OFF`, and non-workspace independent nested `module_root`s completely distinct (`REQ-go-build-selections`, `REQ-evidence-resolution-cache-format`).
   - In `openingCapture` (`served.go`), keep all captures strictly inside `ensureBackend()` before `newContext` opens typed loading (preserving the straddle-check invariant of `REQ-evidence-resolution-freshness-typed-load`), iterate `s.sels` in view-precedence order rather than `sort.Strings(keys)`, skip spawning `selectionEngine` when `remaining` has zero symbols in `s.inventory[key]`, and after capturing the primary `"default"` view (`sel.isDefault() && captureForTest == nil`), filter `remaining` down to symbols not already captured as callables in `"default"` (since `"default"` is always searched first in `b.ResolveIn`, any callable present in `"default"` cannot fall through to a secondary selection).

3. **Batching `Fuzz*` Seed-Replay Targets Under `AssumePure` (`internal/backends/golang/selection.go`)**:
   - `selection.go` previously split `Fuzz*` targets into `len(fuzzes)` separate solo processes whenever mixed with `Test*` so `(*testing.F).Fuzz` (which lacks operation-outcome support in `gofresh`) cannot block `Unit.Register`'s `prepareOutcomeSupport` for sibling `Test*` witnesses that rely on stdlib observation proofs.
   - Under `n.AssumePure == true`, `gofresh` marks all session subjects pure (`len(observed) == 0` in `Unit.Register`), so `prepareOutcomeSupport` is never invoked and `-test.run=^(TestA|...|FuzzX|...)$` replays committed fuzz seeds deterministically inside the single package batch process. Guard the `mixed_fuzz` split behind `!n.AssumePure` (and when `!n.AssumePure` does isolate `fuzzes` from `Test*` subjects, keep sibling observation proofs intact).
