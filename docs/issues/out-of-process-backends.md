# Conditional wire interface for another language backend

Lands: tooling-redesign 9.4

Activation condition: a specific second language backend is planned. The
final census preserves that condition; it does not make a new backend or
wire protocol immediate work.

REQ-backend-surfaces and `internal/arch` define the trusted backend boundary.
The Go resolver already crosses a process boundary through
`internal/backends/golang/resolver.go` and `resolverclient.go`, using a
self-exec child and JSON-line protocol under
REQ-go-owned-processes-resolver-child. The remaining capability is a backend
interface a non-Go implementation can implement, not creating the first
out-of-process resolver.

At activation, derive the resolution, shape, witness and prover protocol
with explicit ownership, error/cancellation and trust semantics. Orchestrated
current execution remains distinct from consuming arbitrary persisted result
claims; REQ-core-claims-untrusted still applies. Gomutant owns mutation
findings. Freeze only the surfaces the actual second backend needs, and
preserve source-derived versus execution-derived authority across the wire.
