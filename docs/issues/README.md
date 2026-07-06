# Issues

Deferred follow-ups. Each carries a `Lands:` trigger saying when it should be pulled in.

- **[proto-backend](proto-backend.md)** — descriptor-level verification via protocompile;
  spec exists, five requirements gapped. *Lands: when a corpus needs wire evidence that
  shape pins and Go witnesses cannot cover.*
- **[term-name-shadowing-lint](term-name-shadowing-lint.md)** — warn when a term name shadows
  another term or a common word. *Lands: when profile lints are extended.*
- **[harden-binding-granularity](harden-binding-granularity.md)** — harden mutates the whole
  bound function, so requirements sharing a symbol report survivors from each other's logic.
  *Lands: when harden gains statement-level attribution, or the union-of-tests expectation is
  documented.*
