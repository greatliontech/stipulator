# Plan: stipulator stops mutation-testing — targets out, findings in

Spec: docs/specs/hardening.md, rewritten across these chunks. The seams are
documents, never imports and never exec: stipulator emits its targets export
(the format it owns; gomutant's adapter parses it) and reads gomutant's
versioned findings document by label. Each tool is complete alone.

- [x] 1. The targets verb: `stipulator targets` (CLI) and a `targets` MCP
  tool emit the export — every go implements-binding with its witness union
  and requirement ids, {"stipulatorTargets":1, targets:[{symbol, witnesses,
  requirements}]} — derived by Plan from the binding store, to stdout or
  --out. hardening.md gains the export clause (the wire format is contract).
- [ ] 2. Findings in: the gate's coverage reminder, views roll-ups, and the
  context dossier read gomutant's findings document (default
  .gomutant/findings.json at the tree root, overridable), pin freshness
  computed with stipulator's own hashes — canon is deliberately
  hash-compatible — and requirement scoping recovered from labels.
  hardening.md re-scopes the reminder and records clauses to the document.
- [ ] 3. The retirement: the harden verb, the MCP harden tool, --ephemeral,
  attest survivor, internal/harden's Run/records/ephemeral, the kill-sheet
  store (records/hardening.go, .stipulator/hardening textprotos, the
  Hardening/HardeningSet proto messages), and the engine half of
  backends/golang/harden.go (Mutants + operators, RunMutant, TestProbe,
  SplitRapidPkgs, BodyHash, fileOf, renderFile) — Vacuous and its helpers
  stay (bind-time policy), Surface stays (staged classification). Plan stays
  (it feeds the targets verb). hardening.md retires the migrated clauses
  (mutation, operators, records' engine half, attestation, ephemeral) as
  gomutant's; what remains: vacuity, the export, the findings reading, the
  reminder, staged scope.
- [ ] 4. Close-out: triage stipulator issue docs; close gomutant's
  docs/issues/stipulator-adoption.md (gomutant side); file the gofresh
  witness-freshness follow-up as its own issue doc; delete this plan.
