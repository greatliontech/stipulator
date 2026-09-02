# Consolidation candidates from the runner-inspectability folds

Accumulated across the five-fold change set (e31795d..25b7dcd) and its
reviews; each is a structural-collapse candidate for the retroactive
automation-and-consolidation audit, recorded here so the audit's walk
starts from findings, not recall.

- **Requirement index built six ways.** `author.Gap`, `author.Editorial`,
  `coverage.Evaluate`, `cmd/pin`, verify's hash walk, and
  `check.GapScope` each build their own id→hash / membership map from
  `spec.GetRequirements()`. One `corpus.Index(spec)` collapses them and
  removes the hazard of a scoped or partial spec reaching a walk where
  a missing id silently reads as drift.
- **Consent-pin discipline in per-kind shapes.** Bindings and gap
  records each carry their own backfill/preserve ladder in
  `records.Pin` and their own re-pin loop in `author.Editorial`. A
  shared "record carrying a content pin" seam would collapse them —
  the editorial unset-pin asymmetry the review caught was representable
  only because the two ladders are written twice.
- **Sentinel substitution/validation split.** `Gaps` substitutes
  `SelfSentinel` before calling `Gap`, whose target validation then
  cannot accept the sentinel its own package documents; the exported
  entry point and the sentinel contract live on opposite sides of the
  call.
- **Bulk declare recompiles per requirement.** `Gaps` loops over
  `Gap`, which compiles the corpus on every call — an N-requirement
  declaration compiles N times.
- **Executor diagnostics trio.** The budget renderer, residue
  classifier, and abort predicate in the execute path share shape and
  could fold (surfaced in the timeout-attribution fold's review).
- **Go-backend load-path pairs.** `workspaceMembers` double-parse with
  two error policies; the `viewErrors`-vs-attribution vocabularies;
  the classifier's double-resolve (surfaced in the load-attribution
  fold's review; the xtest fold partially collapsed via `moduleOwns`).
- **Env-walk vocabularies.** `envIndex`/`sortedKeys` beside
  `lookupEnv`/`setEnv`/`dropEnv` in the witness-env fold.
- **CLI residue.** `check --ids` takes a comma scalar where sibling
  verbs batch flags; `gap`'s value-based `conditioned` guard is less
  precise than flag presence (surfaced as nits in the claim-batching
  fold's review).

Lands: with cross-tool train chunk 136 (gofresh
docs/plans/cross-tool-train.md — the retroactive
automation-and-consolidation audit; its stipulator walk folds or files
each candidate).
