# Evaluate Shared CLI/MCP/Process Utility Module (`internal/guidance`, `internal/resident`, `internal/toolversion`, `internal/shortgates`, `internal/shapecorpus`, `internal/gotool`, `internal/trace`)

- **Status:** Open (user-authorized Option 1A internalization; evaluate Option 1B shared module when changes cross tools)
- **Lands:** Next cross-tool CLI/MCP/process utility revision across `pew`, `stipulator`, and `gomutant`

## Context

When non-freshness subpackages were evicted from `github.com/greatliontech/gofresh` so `gofresh` exports only its single root freshness package, `stipulator` internalized its copies of:

- [`internal/guidance`](../../internal/guidance/guidance.go) — embedded CLI/MCP `docs/guidance.md` parser and pflag/JSON-Schema projection helpers
- [`internal/resident`](../../internal/resident/resident.go) — `/proc` RSS, cgroup memory-ceiling, and CPU-capacity admission helpers
- [`internal/toolversion`](../../internal/toolversion/version.go) — `runtime/debug.ReadBuildInfo` version formatting
- [`internal/shortgates`](../../internal/shortgates/shortgates.go) — `testing.Short()` AST discipline test helper
- [`internal/shapecorpus`](../../internal/shapecorpus/shapecorpus.go) — Go declaration-shape test corpus
- [`internal/gotool`](../../internal/gotool/gotool.go) — `go` process-group containment (`Containment`, `Runner.Program`), `GOVERSION` sampler (`Sampler`, `SnapshotSampler`), and `Salvaged` helper
- [`internal/trace`](../../internal/trace/trace.go) — zero-disabled-cost structured diagnostic tracing (`GOFRESH_TRACE` / `GOFRESH_TRACE_FILE`) interleaved with `gofresh`

## Consolidation Evaluation (Option 1B)

`pew`, `stipulator`, and `gomutant` currently maintain internal copies of the subset of these packages each tool uses. When any of these utilities requires a synchronized change across multiple tools, evaluate extracting them into a dedicated shared CLI/MCP/process utility module (Option 1B) outside `gofresh`.
