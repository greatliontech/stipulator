# Plan: hot-loop serving — check serves fresh, reuse is visible, agents are first-class

Spec: `docs/specs/overview.md`, `docs/specs/change.md`, `docs/specs/evidence.md`,
`docs/specs/backends/go.md`. `stipulator check` becomes the fast warm-loop verb (serving
proven-fresh witnesses by default, `--full` forcing whole-suite execution), the machine-local
witness cache moves to the user cache directory, every re-run is explainable, and the MCP face
is designed for its actual consumer: a token-conscious agent inside a harness, not a human
reading a terminal.

- [ ] 1. Triage gate; gofresh bump to current
- [ ] 2. Serving `check`: serve-fresh default, `--full` for whole-suite execution (execution-contract spec amendment)
- [ ] 3. Witness cache relocation: user cache directory keyed by corpus root, per-entry files with atomic installs; in-repo `.stipulator/cache` and its gitignore handling removed (clean break)
- [ ] 4. Freshness visibility: per-test served/executed/uncacheable reasons on check and witness output, moved-input attribution via per-input digests
- [ ] 5. Record lifecycle ownership — gap surface: upsert/retract verbs with atomic batch apply (CLI file + MCP batch parity), retraction working on dangling records (requirement already gone) without tombstoning, repeatable requirements on the MCP tool, bulk self-landing sentinel, fired-bit expressibility; verify reports dangling gaps with their repair command; `prune --dangling` (with `--check`) as the explicit bulk repair, never part of ordinary resolved-record pruning
- [ ] 6. Agent-first MCP face: server instructions and tool descriptions that teach when to use what; responses restructured for token economy with next-action guidance; guided root-discovery failure; check JSON/quiet analogs; progress-token leverage audited across every long-running tool - each arms the progress seam with no witnessing or analysis stretch silent past a client tool deadline (a real agent's check died at its 600s cap this way)
- [ ] 7. Output and remediation audit: every message earns its lines; mechanical remediations (e.g. healing unbound registrations) become offered actions instead of imperative instructions
- [ ] 8. Concurrent record writes: compare-and-swap at the verb layer
- [ ] 9. Close-out gate
