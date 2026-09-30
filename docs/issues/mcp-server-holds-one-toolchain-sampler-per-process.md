# The MCP server holds one toolchain sampler for its whole process

gofresh's toolchain sampler (gotool.Sampler) memoizes one sample per
(directory coordinate, normalized environment) for the sampler's
lifetime, and REQ-fresh-toolchain-skew (amended at gofresh chunk
281.C, 2026-09-30) states that lifetime as one judged operation: a
long-lived consumer holds one sampler per operation, never per
process, because a toolchain replaced under a living memo — the `go`
on PATH swapped in place, a go.mod `toolchain` line moved to a release
GOTOOLCHAIN=auto now selects — is judged by its predecessor's sample,
and the language-series skew the clause requires the composite to
refuse never reaches ToolchainSkew (a wrong-pass). The failed-sample
memo has the mirror fault: one transient failure refuses until the
process restarts.

stipulator holds its sampler per process
(internal/backends/golang/provenance.go:42) — one operation for the
CLI, which is one process per verb, but every check, gate, verify and
partitions call of the MCP server for the server's whole life. The
fix: the sampler minted per served operation (the composite over it),
the CLI unchanged in effect.

Lands: cross-tool train chunk 290 (stipulator's bump behind gofresh
281, which carries the amended clause and the composite's constructor).
