# Issues

Deferred follow-ups. Each carries a `Lands:` trigger saying when it should be pulled in.

- **[proto-backend](proto-backend.md)** — descriptor-level verification via protocompile;
  spec exists, five requirements gapped. *Lands: when a corpus needs wire evidence that
  shape pins and Go witnesses cannot cover.*
- **[gopter-property-recognition](gopter-property-recognition.md)** — gopter-driven tests
  classify as example witnesses; the classifier recognizes fuzz targets and rapid drivers
  only. *Lands: when a corpus standardized on gopter needs invariant coverage.*
- **[out-of-process-backends](out-of-process-backends.md)** — the backend surfaces (and the
  mutator, whose kill-sheet records are already the interchange contract) can move behind a
  wire protocol with the trust model intact; deferred while one backend exists. *Lands: when
  a second language backend is planned.*
- **[slice-frontier-uncertainty](slice-frontier-uncertainty.md)** — typed frontiers miss
  reflection, build tags, and init effects; pew's closure model (sound floor, provably-safe
  refinement, resolve/widen/unverifiable dispositions) is the reference shape. *Lands: when a
  corpus relies on slice completeness for automated context assembly over such code.*
- **[prover-trust-tiers](prover-trust-tiers.md)** — the proof rung assumes near-sound provers;
  a heuristic analyzer must not inherit it. *Lands: when a heuristic analyzer prover is
  proposed.*
- **[witness-subset-adequacy](witness-subset-adequacy.md)** — union sheets cannot say whether
  one requirement's own witnesses have teeth; an opt-in per-requirement probe is measurable
  without attribution claims. *Lands: when a requirement's risk profile demands per-requirement
  witness adequacy.*
- **[concurrent-record-writes](concurrent-record-writes.md)** — record verbs are last-writer-
  wins; concurrent agents need compare-and-swap at the verb layer, never actor metadata in
  records. *Lands: when concurrent agents write records in one working tree.*
- **[performance-evidence-axis](performance-evidence-axis.md)** — no clause kind or evidence
  class measures performance; pew recordings (guard-derived validity) are the binding-pin
  model applied to measurements and slot in without bending the trust model. *Lands: when a
  corpus declares a performance requirement.*
- **[analyzer-witness-hardening](analyzer-witness-hardening.md)** — harden mutates function bodies
  only, so analyzer witnesses get no teeth check; add a structural mutation class (inject a
  forbidden import / break an asserted method set, require the witness to fail). *Lands: when an
  analyzer witness needs adequacy evidence.*
- **[harden-ephemeral-mutants](harden-ephemeral-mutants.md)** — one-off manual mutants have no
  standardized apply-run-restore evidence path. *Lands: when a consuming repo needs to prove a
  manual mutation that harden cannot generate.*
- **[harden-integration-recipes](harden-integration-recipes.md)** — generated-data drift,
  parser-guard, resolver-precedence, and caller-mapping mutants recur as recipes outside the body
  operator set. *Lands: when a corpus repeatedly needs manual mutants for generated data,
  resolver seams, or caller mappings.*
- **[harden-staged-deleted-surface](harden-staged-deleted-surface.md)** — `harden --staged-diff`
  labels a deleted or unloadable `.go` surface as an integration seam, conflating a removed
  bound implementation with an out-of-body edit. *Lands: when the coverage-delta reminder
  (harden-new-coverage-reminder) lands.*
- **[harden-new-coverage-reminder](harden-new-coverage-reminder.md)** — gate/verify do not remind
  when newly covered requirements have no fresh hardening sheet. *Lands: when gate/verify output
  is used to drive the full adversarial loop for a change set.*
- **[term-matcher-ascii-boundaries](term-matcher-ascii-boundaries.md)** — `\b` is ASCII-only,
  so non-ASCII term names may never match a use site (silently missing uses-term edges); the
  lint mirrors the same semantics deliberately — fix both together on rune boundaries. *Lands:
  when a corpus declares non-ASCII term names.*
