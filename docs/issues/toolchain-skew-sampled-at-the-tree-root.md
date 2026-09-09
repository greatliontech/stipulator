# The toolchain skew sample reads the tree root and degrades a failed sample

REQ-fresh-toolchain-skew (gofresh docs/specs/overview.md) requires the
ambient toolchain sampled by `go env GOVERSION` in the target module's
directory — under GOTOOLCHAIN=auto the selected toolchain is per module,
so a workspace member declaring a newer toolchain skews while the root's
sample agrees — and a failed sample to refuse. The Go backend samples
the tree root under the group's normalized environment
(internal/backends/golang/provenance.go's provenance probe) and, on a
failed sample, returns nil by design, degrading to a per-view refusal.
Both halves diverge from the clause the other two consumers meet; the
tree-root sample is the exact case the clause's parenthetical names.

Lands: cross-tool train chunk 221 (soundness conformance between the
policy record and the engine — the provenance probe is already in its
charter).
