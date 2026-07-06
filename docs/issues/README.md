# Issues

Deferred follow-ups. Each carries a `Lands:` trigger saying when it should be pulled in.

- **[proto-backend](proto-backend.md)** — descriptor-level verification via protocompile;
  spec exists, five requirements gapped. *Lands: when a corpus needs wire evidence that
  shape pins and Go witnesses cannot cover.*
- **[term-name-shadowing-lint](term-name-shadowing-lint.md)** — warn when a term name shadows
  another term or a common word. *Lands: when profile lints are extended.*
- **[gopter-property-recognition](gopter-property-recognition.md)** — gopter-driven tests
  classify as example witnesses; the classifier recognizes fuzz targets and rapid drivers
  only. *Lands: when a corpus standardized on gopter needs invariant coverage.*
- **[determinism-witness-operation-coverage](determinism-witness-operation-coverage.md)** —
  the determinism property quantifies compile→verify→evaluate and the record verbs; fmt,
  bundle, facts, diff, and harden have no determinism witness. *Lands: when the determinism
  harness chunk of the active plan begins.*
- **[kill-sheet-attribution](kill-sheet-attribution.md)** — RunMutant counts any test failure
  (and timeouts, by design) as a kill, so sheet counts are not reproducible under noise; two
  same-pinned sheets disagreed by 15 mutants. *Lands: when the determinism harness chunk of
  the active plan begins.*
- **[out-of-process-backends](out-of-process-backends.md)** — the backend surfaces (and the
  mutator, whose kill-sheet records are already the interchange contract) can move behind a
  wire protocol with the trust model intact; deferred while one backend exists. *Lands: when
  a second language backend is planned.*
- **[kill-sheet-environment-pin](kill-sheet-environment-pin.md)** — sheets pin every input
  stipulator controls but not the toolchain; a Go upgrade silently invalidates measurements.
  *Lands: when the determinism harness chunk of the active plan begins.*
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
- **[registration-witnesses-unconsumed](registration-witnesses-unconsumed.md)** — runtime
  registrations are crosschecked and reported but never consumed as evidence; the spec's
  "or registered at runtime" witness sentence and the conservative implementation diverge
  exactly on table-driven tests. *Lands: when coverage evidence derivation is next touched.*
