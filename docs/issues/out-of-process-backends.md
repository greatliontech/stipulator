# Out-of-process backends (and an extractable mutator)

Lands: when a second language backend is planned.

The backend abstraction (REQ-backend-surfaces; core neutrality proven by
`internal/arch`) is in-process Go today. When a second language becomes
real, the same surfaces — symbol resolution, shape hashing, witness
running, provers, and the harden primitives — can move behind a wire
protocol (gRPC service definitions are natural for this proto-first
project) with the trust model intact: evidence stays derived in the
current run because stipulator orchestrates the run; only the process
boundary moves. What must NOT move to a file interchange is the witness
run itself — a persisted result consumed as evidence violates
REQ-core-claims-untrusted.

Mutation testing is separable on easier terms whenever wanted: kill-sheets
are advisory records (REQ-harden-exploration) with symbol/body-hash/
witness-set pins, so the per-symbol `HardeningSet` is already the
interchange contract an external mutator would write; stipulator verifies
the pins, never the counts. Deliberately deferred: freezing the still-fast-
moving backend interface into a wire contract taxes every extension (three
optional surfaces were added across chunks 16–19 alone) and buys nothing
while exactly one backend exists.
