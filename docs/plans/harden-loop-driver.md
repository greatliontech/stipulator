# Plan: harden as adversarial-loop driver

Derived from `docs/specs/hardening.md`. Makes `harden` usable as the
adversarial-loop entry point for a staged change set: it can explain what of
the delta it covers, what needs manual mutation, and it can record the manual
mutants it cannot generate. All three surfaces are exploration/reminder output
— none feed the gate (REQ-harden-exploration).

Design decision pinning the whole theme: the delta is measured **relative to
HEAD**, checkout-free. The current side is `os.DirFS` (what the compiler
already reads); the HEAD side, where needed, is `gitfs.FS(HEAD)` (exists); the
changed-file list comes from the git index. Freshness of hardening is read
from kill-sheet staleness (a body-hash move re-stales a sheet — already
defined), so no base-commit body-hash comparison and no worktree are needed.

Resolves issue docs: `harden-ephemeral-mutants` (chunk 3).

## Chunks

- [x] **1. `harden --staged-diff` surface classification.** New spec clause
  `REQ-harden-staged-scope`. Walk the HEAD-relative changed files, map to bound
  symbols, classify each surface: covered-by-harden / unbound-impl /
  no-witness / witness-class-outside-operators / generated-or-data /
  integration-seam. Report on CLI + MCP; not a gate. Reuses `harden.Plan`,
  `Backend.WitnessClass`, `Backend.generated`.
- [x] **2. gate/verify new-coverage-lacks-hardening reminder.** New spec clause
  (reminder, explicitly not a gate). List covered requirements whose
  implementing symbol has no fresh (non-stale) kill-sheet; split "harden can
  run this" from "no harden target → see staged-diff". Reuses chunk 1's
  classifier and kill-sheet staleness. Test: gate verdict invariant to the
  reminder.
- [ ] **3. ephemeral mutant runner.** New spec clause `REQ-harden-ephemeral`.
  Accept a patch + test command, apply through a build overlay, require the
  command to fail, restore, emit an evidence record. Reuses `golang.RunMutant`.
