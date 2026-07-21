# Plan: stipulator issue burn-down

Spec: `docs/specs/` (canonical per touched domain: evidence.md for the
witness path, change.md for impact, mcp.md for tool surfaces). Eliminates
the standing issue inventory on the now-proven serving substrate; every
chunk runs the adversarial loop and the corpus check stays green.

- [x] 1. Witness concurrency knob: reviewed policy bound + pressure-honest
  default (witness-concurrency-saturates-host)
- [x] 2. Witness store GC: a maintenance surface evicting departed
  identities' variants (witness-store-gc)
- [x] 3. Check-result wire cleanup: one home for package failures
  (check-result-duplicate-package-failure; its Lands fires by this change)
- [x] 4. Symlinked member validation: resolve or refuse escaping members
  (symlinked-member-escapes-lexical-validation)
- [x] 5. Empty binding-surface guidance (targets-empty-surface-lacks-guidance)
- [x] 6. Gap violation-class scoping (standing-gap-absorbs-unrelated-red)
- [x] 7. Impact-preview spec-amend candidates: dispose both
  (impact-preview-omission-bounds)
- [x] 8. Go module-rename bulk symbol retarget
  (go-module-rename-lacks-symbol-migration)
- [ ] 9. Prune serving-class pin: assess the seam, pin or redefer
  (prune-serving-class-unpinned)
- [ ] 10. Final sweep: explicit disposition per remaining condition-parked
  doc (design-tier features whose triggers have not fired; externally
  gated items), then close-out gate - full suite, release, corpus
  double-check green
