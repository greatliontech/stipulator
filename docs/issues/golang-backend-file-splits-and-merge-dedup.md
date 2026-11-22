# Decompose Multi-Concern `golang` Backend Files (`witnessrun.go`, `derive.go`) & Deduplicate Post-Run Merge Helpers

- **Status:** Open
- **Governing contract:** [`docs/specs/evidence.md`](../specs/evidence.md), [`docs/specs/backends/go.md`](../specs/backends/go.md)
- **Lands:** Chunk 4 in [`docs/plans/plan.md`](../plans/plan.md)

## Problem

1. **Multi-Concern 1,000+ Line Files in `internal/backends/golang`**
   - [`internal/backends/golang/witnessrun.go`](../../internal/backends/golang/witnessrun.go) (`1,116` lines) mixes selective witness execution orchestration and `TestRun` assembly (`lines 1–82, 146–842`) with `witnessGroup` historical-variant round preparation, runtime checking, and inert-compartment-growth ledger refresh (`lines 83–144, 844–1116`).
   - [`internal/backends/golang/derive.go`](../../internal/backends/golang/derive.go) (`1,013` lines) mixes `SuiteHealthy` / `DeriveTestRun` (`lines 1–169`), `Capture` / `CapturePolicy` / group & linker identity segment derivation (`lines 171–864`), and `gofresh.Session` engine construction / diagnostics (`lines 865–1013`).

2. **Duplicated Post-Run Merge, Publish-Reconciliation & Formatting Helpers Across `package golang`**
   - `DeriveTestRun` ([`derive.go`](../../internal/backends/golang/derive.go) lines 106–166) and `execMerge.consumeMerge` ([`execmerge.go`](../../internal/backends/golang/execmerge.go) lines 559–611) duplicate test-outcome rank selection, subject registration, and failure diagnostic folding.
   - `witnessrun.go` (`lines 723–766`) and `recorder.go` (`lines 207–236`) duplicate post-run `publishedKey` / `Uncached` / `UncacheableReasons` reconciliation.
   - `countWord` (`served_account.go`) and `plural` (`witnessrun.go`) are byte-for-byte identical helpers in `package golang`.

## Resolution Scope

1. Split `witnessrun.go` (`witnessrun.go`, `witnessgroups.go`) and `derive.go` (`derive.go`, `capture.go`, `engine.go`) within `package golang` (preserving all `.stipulator/bindings/` symbols and `internal/arch/arch_test.go` interface proofs).
2. Extract shared helpers for test-outcome rank/diagnostic folding (`derive.go` + `execmerge.go`), post-run `Uncached` / `UncacheableReasons` reconciliation (`witnessrun.go` + `recorder.go`), and pluralized count formatting (`served_account.go` + `witnessrun.go`).
