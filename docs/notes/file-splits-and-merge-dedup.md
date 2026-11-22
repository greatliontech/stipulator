# Chunk 4 Working Notes: Cohesive File Splits (`<= ~1,050` LOC Repo-Wide) & Post-Run Merge Deduplication

## Governing Contracts & Structural Targets

1. **Repo-Wide `<= ~1,050` LOC Production File Bound (`AGENTS.md` #4, `docs/issues/golang-backend-file-splits-and-merge-dedup.md`):**
   - Only 4 handwritten non-test `.go` files in `stipulator` exceed `1,000` LOC (`guidance.go` `1,150`, `witnessrun.go` `1,128`, `coverage.go` `1,070`, `derive.go` `1,014`); the 5th largest is `seeding.go` (`992` LOC).
   - Split all 4 along cohesive responsibility seams within their existing packages (preserving 100% of `.stipulator/bindings/` symbol names and signatures so zero static bindings drift):
     - `internal/guidance/guidance.go` (`1,150` LOC) -> `guidance.go` (model types + markdown fence-aware parser: `Document`, `Verb`, `SurfaceName`, `Knob`, `Parse`, `parseVerb`, `parseSurfaceList`, `parseKnob`) + `render.go` (surface projections, `Coverage`, `Clause`, `Embedded`, `Registration`, `Usage`, `DescribeSchema`, `PrintsDefault`, `Served`, `defaultSpellingDefect`).
     - `internal/coverage/coverage.go` (`1,070` LOC) -> `coverage.go` (evaluation types, `GapTally`, `Evaluate`, `rowFacts`, `excuseSet`, `redClasses`, `bucketNames`, `excuseNames`) + `policy.go` (`Policy`, `PolicyFromManifest`, `Active`, `Overridden`, `AdmitsAttestation`, `RequiredEvidence`, `satisfied`, `KindHint`, `requiredEvidence`, `manualUnfired`, `ConditionText`, `conditionHolds`).
     - `internal/backends/golang/witnessrun.go` (`1,128` LOC) -> `witnessrun.go` (`RunWitnesses`, `RunWitnessesPolicy`, `RunWitnessesScoped`, `runWitnesses`, `quiesceSeeding`) + `witnessgroups.go` (`witnessGroup`, `packages`, `servedIn`, `selectedRecord`, `roundCandidates`, `prepareWitnessGroups`, `servingCandidates`, `compartmentGrownRefresh`).
     - `internal/backends/golang/derive.go` (`1,014` LOC) -> `derive.go` (`SuiteHealthy`, `invocationFacts`, `indexInvocations`, `DeriveTestRun`, plus shared `foldTestRows`, `foldTestDiagnostics`, `reconcileUncached`, `plural`), `capture.go` (`groupPackage`, `captureGroup`, `WitnessRecorder`, `Capture`, `policyDiscovery`, `CapturePolicy`, `LoadCapture`, `ObligationUniverse`, `classifySeeded`, `groupKey*`, `groupIdentity*`, `LiveGroupDigests`, `discoverPolicy`, `groupSubjects`), and `engine.go` (`beforeGroupEngineForTest`, `groupSessionState`, `groupSession`, `groupEngine`, `newEngine`, `engineDiagnostics`, `progressDiagnostic`, `diagnosticsTo`, `emitEngineDiagnostic`).

2. **Post-Run Merge, Reconciliation & Count-Formatting Deduplication (`package golang`):**
   - Extract `foldTestRows` and `foldTestDiagnostics` in `derive.go`, sharing test-outcome rank selection, runtime registration collection, `ranTop` tracking, and test-scoped failure diagnostic folding across `DeriveTestRun` (`derive.go`) and `consumeMerge` (`execmerge.go`).
   - Extract `reconcileUncached` in `derive.go`, sharing `publishedKey` / `servedKey` / `Uncached` / `UncacheableReasons` reconciliation across `runWitnesses` (`witnessrun.go`) and `(*WitnessRecorder).Derive` (`recorder.go`).
   - Consolidate `countWord` (`served_account.go`) and `plural` (`witnessrun.go`) onto one `plural(n int, noun string) string` in `derive.go`.
   - Delete resolved `docs/issues/golang-backend-file-splits-and-merge-dedup.md` and its entry in `docs/issues/README.md`.
