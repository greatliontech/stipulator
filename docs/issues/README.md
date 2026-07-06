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
