# Chunk 2 Working Notes: Ground-Up `Served` & `resolutioncache` Re-Derivation onto Multi-View `gofresh.SourceResult`

## Scope & Goals

1. Update `REQ-evidence-resolution-freshness`, `REQ-evidence-resolution-freshness-typed-load`, and `REQ-evidence-resolution-cache-format` (`version = 7`) in `docs/specs/evidence.md` (`old -> new`) so ordered multi-view `gofresh.SourceResult` entries (`views`) certify callable and non-callable Go declarations uniformly, including earlier-view declaration absence, cross-view seeding helpers, classifier executable identity, and closing re-observation.
2. Re-derive `internal/resolutioncache` (`version = 7`) around `Record.Views []ViewEntry` (`{Selection string, Present bool, Fingerprint witnesscache.Fingerprint}` under `gofresh.SourceResult`), retiring `Certificate`, `Derivation`, and `SourceTiers`, and update external fixture callers (`internal/prunetest/store.go`, `internal/prune/cache_test.go`).
3. Re-derive `internal/backends/golang` (`Served`) onto one `gofresh.Session` per `buildSelection` (`gofresh.SourceResult` + `CheckingUnit` straddle re-observation + per-package load-failure isolation + `"stipulator/source-classifier=" + sourceClassifierIdentity` in `BuildDescription.Inputs`), delete `sourcecertificate.go` (`457` LOC), `selectioncontext.go`'s `sourceGraphs` (`~135` LOC), and `packageinventory.go`'s `parseNonCallableSymbols` / `inventoryFiles` (`~85` LOC), and split `served.go` into cohesive `served.go`, `served_capture.go`, and `served_account.go` (`<= ~1,050` LOC each).

## First-Principles Architectural Derivations

- **Uniform Callable & Non-Callable `gofresh.SourceResult` Representation**:
  - `gofresh.SourceResult` natively scans and hashes both callable symbols (`func`, methods) and non-callable declarations (`type`, `var`, `const`, struct fields `T.N`, interface methods) via `staticDecl` (`ComputeMaximalFactsBatch` + `TestVariantClosure` + `ToolchainContent` + `AdmissionPolicy` + `Guards.{Toolchain,BuildConfig}`), omitting runtime, purity, observation, and dynamic-state fields by construction.
  - Consequently, `Record` needs neither `SourceTiers` projection nor a zero-fingerprint special case for non-callables, and `Served` needs no parallel `parseNonCallableSymbols` AST scanner.
- **Ordered Multi-View Precedence & Cross-View Seeding Soundness**:
  - A symbol resolved in `r.Selection` under ordered policy selections `[sel_0, ..., sel_{k-1}]` records `Views` in exact policy order:
    - Every view `i < idx(r.Selection)` has `Present == false` and zero `Fingerprint` (certifying declaration-precedence absence).
    - View `r.Selection` has `Present == true` and its `SourceResult` `Fingerprint`.
    - Every view `i > idx(r.Selection)` has `Present == true` with its `SourceResult` `Fingerprint` (covering cross-view seeding helpers in `TestImports`/`Deps` via `gofresh` core closure) or `Present == false` when not declared in that view.
  - Passing `"stipulator/source-classifier=" + sourceClassifierIdentity` in `BuildDescription.Inputs` binds the analyzer binary's image digest directly into `Guards.BuildConfig`.
- **Closing Interval Re-Observation & Per-Package Isolation**:
  - Opening `SourceResult` `CheckingUnit`s per selection and calling `CheckingUnit.Close` after all selections complete (across `beforeServingCloseForTest`) re-observes source, `go.mod`/`go.work`, and `GOENV` via `gofresh`'s native `closeValidationReobservation`, revoking affected served symbols with `"source dependencies moved during serving"` on `ErrViewChanged`.
  - For any served record relying on `Present == false` in a view, closing re-verifies package inventory and subject absence in that view so an earlier declaration introduced mid-serving is caught.
  - When a multi-package `Snapshot(..., SourceResult)` fails on a broken package, `captureSourceView` falls back to one `Snapshot` per package so unaffected packages still serve/capture while each broken package is queried once and degraded with its affected symbols named; if all packages fail, one whole-selection degradation notice is emitted.
