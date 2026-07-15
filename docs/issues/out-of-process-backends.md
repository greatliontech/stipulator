# Out-of-process backends

Lands: when a second language backend is planned.

The backend abstraction (REQ-backend-surfaces; core neutrality proven by
`internal/arch`) is in-process Go today. When a second language becomes
real, the same surfaces — symbol resolution, shape hashing, witness
running, and provers — can move behind a wire
protocol (gRPC service definitions are natural for this proto-first
project) with the trust model intact: evidence stays derived in the
current run because stipulator orchestrates the run; only the process
boundary moves. What must NOT move to a file interchange is the witness
run itself — a persisted result consumed as evidence violates
REQ-core-claims-untrusted.

Mutation testing remains outside this backend boundary: Stipulator exports
binding surfaces, while gomutant owns mutation findings and their freshness.
Freezing the still-moving trusted backend interface into a wire contract is
deferred while exactly one backend exists.
