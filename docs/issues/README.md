# Open Issues

- [`golang-backend-file-splits-and-merge-dedup.md`](golang-backend-file-splits-and-merge-dedup.md) — Decompose multi-concern `internal/backends/golang` files (`witnessrun.go` 1,116 lines, `derive.go` 1,013 lines) along cohesive responsibility seams and deduplicate post-run test-outcome merge, publish-reconciliation, and count-formatting helpers (Lands: Chunk 4 in [`docs/plans/plan.md`](../plans/plan.md)).
- [`shared-cli-mcp-process-utilities.md`](shared-cli-mcp-process-utilities.md) — Evaluate extracting internalized CLI/MCP/process utilities (`internal/guidance`, `internal/resident`, `internal/toolversion`, `internal/shortgates`, `internal/shapecorpus`, `internal/gotool`, `internal/trace`) into a dedicated shared module (Option 1B) on the next cross-tool utility change.
