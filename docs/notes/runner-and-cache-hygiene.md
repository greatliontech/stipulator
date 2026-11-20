# Chunk 1 Working Notes — Runner-Owned Ephemeral Exclusions, Immediate `testlog` Removal, `witnesscache` GC/Batch Install & `internal/testenv`

## Governing Contracts & Scope

- **Specs:**
  - `docs/specs/evidence.md`:
    - `REQ-evidence-witness-freshness-inputs` (runner-owned vs. reviewed observation exclusions, complete package-process environment preservation)
    - `REQ-evidence-witness-freshness-exemptions` (producing environment's `TMPDIR` / `GOTMPDIR` ephemeral root exemption)
    - `REQ-evidence-witness-freshness-diagnosable` (`round == 0` refusal attribution when all variants fail)
    - `REQ-evidence-store-gc` & `REQ-evidence-witness-cache-refusals` (`witnesscache.GC` `loadEntry` alignment and surviving-record ledger retention)
  - `docs/specs/backends/go.md`:
    - `REQ-go-retained-test-execution` (retained binary lifecycle and zero temporary-storage requirement when `TMPDIR` is absent/unwritable)
    - `REQ-go-owned-processes-telemetry-home` (runner-owned `XDG_CONFIG_HOME` when `n.TelemetrySource != ""`)
- **Retires Issue:** `docs/issues/retained-artifact-scratch-and-testlog-lifecycle.md`

## First-Principles Design & Contract Alignment

1. **Runner-Owned Ephemeral `ExcludedPaths` (`docs/specs/evidence.md`, `internal/backends/golang/nativeexecution.go`, `linker.go`)**:
   - Expand `REQ-evidence-witness-freshness-inputs` so runner-owned exclusions (beyond `.` and `.git`) cover the attempt's own retained test binary/work directory (`artifact.root`, `artifact.Path`, `artifact.work`), per-attempt runtime coverage directory (`runtime.root`, `runtime.CoverageFragment`), and the runner-owned telemetry config home (`XDG_CONFIG_HOME` only when `n.TelemetrySource != ""`).
   - Emit both clean and `filepath.EvalSymlinks`-resolved spellings (converting in-tree paths to tree-relative slash form so `gofresh`'s `pathRel` classifier matches them if `TMPDIR` lies inside the verification tree), passed **only** on `gofresh.ProcessDescription.ExcludedPaths` / `ProducerIngest.ExcludedPaths` — never mutating `n.ExcludedPaths` or `witnesscache.Record.ObservationExclusions`.

2. **Preserving Child `TMPDIR` Verbatim (`REQ-evidence-witness-freshness-inputs`, `REQ-evidence-witness-freshness-exemptions`, `REQ-go-retained-test-execution`) & Immediate `stipulator-testlog-*.txt` Removal**:
   - `gofresh`'s `resolvedRoots.ephemeralRoots("", treeRoot)` (`gofresh/internal/runtimeinput/roots.go:43-56`) already declares the producing environment's `TMPDIR` (or `/tmp`) and `GOTMPDIR` as ephemeral temp roots, eliding cleaned `t.TempDir()` / `os.CreateTemp("", ...)` reads automatically.
   - Overriding child `TMPDIR` with a per-attempt `stipulator-scratch-*` directory would violate three explicit contracts and two bound witness tests:
     1. `REQ-evidence-witness-freshness-inputs` requires the retained binary, observation ingestion, and warm checks to share the same complete package-process environment (`getenv TMPDIR` logged by `os.TempDir()` would stale every warm run if `TMPDIR` changed per attempt, while withholding `TMPDIR` from ingestion violates `REQ-inputs-producer-facade`).
     2. `REQ-evidence-witness-freshness-exemptions` (`TestGoRunWitnessesExemptionBoundariesStayObserved`) requires `os.TempDir()` inside witnesses to resolve the producing environment's `TMPDIR` verbatim so pre-existing files beneath `TMPDIR` stay observed.
     3. `REQ-go-retained-test-execution` (`TestRetainedOrdinaryExecutionNeedsNoNewTemporaryStorage`) requires ordinary execution to succeed without allocating temporary storage when `TMPDIR` points to a nonexistent path.
   - Therefore, preserve child `TMPDIR` verbatim per the governing spec, and unlink `a.logPath` (`stipulator-testlog-*.txt`) immediately after `closeNativeProcess` completes in `finishNativeRun` (freeing multi-GiB `/tmp` files per package rather than retaining them across `unit.solos` / invocation lifetime, while retaining `attempt.release()` fallback on early abort).

3. **Primary (`round == 0`) Variant Refusal Preservation, `witnesscache.GC` Admission Alignment & `InstallBatch` (`witnessrun.go`, `witnesscache.go`, `recordstore.go`, `packageleg.go`)**:
   - In `prepareWitnessGroups` (`witnessrun.go`), preserve `round == 0`'s refusal in `wg.executedWhy[s]` unless no earlier round recorded a refusal (later `Valid` rounds still delete `wg.executedWhy[s]`).
   - In `gcSince` (`witnesscache.go`), evaluate `loadEntry(name, data, dir).record != nil` instead of `decodeRecord`, sweeping envelopes whose fingerprint structure or manifest version (`v2`) `Load` permanently refuses; if `store.Sweep` encounters a removal error (`err != nil`), re-scan surviving store files and retain their `ledgerReference` digests before calling `sweepLedgers` (skipping `sweepLedgers` if the re-scan itself fails) so no surviving file ever loses its compartment ledger.
   - Add `recordstore.Store.BoundVariants` and `witnesscache.InstallBatch`: pre-validate `fileName`, ledger validity, and JSON marshaling per record before acquiring the store lock, install each distinct valid `ledgerCoordinate` once, write valid records independently isolating per-record write failures without aborting siblings, and run a single `BoundVariants` directory scan for the batch.

4. **Test-Only `internal/testenv` (`NeutralAmbient`, `GoBuildCache`, `IsolateMain`)**:
   - Extract `NeutralAmbient`, `GoBuildCache`, and `IsolateMain` / `RunMain` into `internal/testenv` (backed by `gotool.GoBuildCache`), deleting production `ambientGoCache` and `NeutralAmbient` from `internal/backends/golang/normalize.go`.
   - Use direct string-literal constant keys (`"GOCACHE"`, `"XDG_CACHE_HOME"`, `"XDG_STATE_HOME"`) in `os.Setenv` calls inside `testenv` so `gofresh`'s `tracked_chdir` `TestMain` static check (`isAllowedNonPWDConstEnvKey`) remains `true`.
   - Wire `testenv.RunMain` / `testenv.IsolateMain` into `TestMain` across `internal/cmd`, `internal/backends/golang`, `internal/witnesscache`, `internal/mcpserver`, and `internal/check`, and remove duplicate `neutralAmbient` wrappers and `interrupt_unix_test.go`'s `go env GOCACHE` subprocess call.
