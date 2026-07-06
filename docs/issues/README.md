# Issues

Deferred follow-ups. Each carries a `Lands:` trigger saying when it should be pulled in.

- **[proto-backend](proto-backend.md)** — descriptor-level verification via protocompile;
  spec exists, five requirements gapped. *Lands: when a corpus needs wire evidence that
  shape pins and Go witnesses cannot cover.*
- **[term-name-shadowing-lint](term-name-shadowing-lint.md)** — warn when a term name shadows
  another term or a common word. *Lands: when profile lints are extended.*
- **[harden-binding-granularity](harden-binding-granularity.md)** — harden mutates the whole
  bound function, so requirements sharing a symbol report survivors from each other's logic.
  *Lands: 21.*
- **[gopter-property-recognition](gopter-property-recognition.md)** — gopter-driven tests
  classify as example witnesses; the classifier recognizes fuzz targets and rapid drivers
  only. *Lands: when a corpus standardized on gopter needs invariant coverage.*
- **[determinism-witness-operation-coverage](determinism-witness-operation-coverage.md)** —
  the determinism property quantifies compile→verify→evaluate and the record verbs; fmt,
  bundle, facts, diff, and harden have no determinism witness. *Lands: when the determinism
  harness chunk of the active plan begins.*
