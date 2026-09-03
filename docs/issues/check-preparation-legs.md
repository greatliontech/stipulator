# Three refusal classes still fire after the work they make moot

REQ-check-preparation (change.md) names five refusal classes that
decide from held inputs; the policy record's static faults decide at
acceptance, and these three still wait behind a witness run:

- the manifest's duplicated coverage cell (internal/coverage
  PolicyFromManifest) is read after the whole witness run in check,
  gate, prune, and the MCP gate and context tools;
- gate's and verify's unknown view, bucket, and filter words
  (internal/views) are parsed after the run on the CLI, where the MCP
  check tool already validates them first;
- the record-hygiene half of verification (verify.Run's problems that
  need no witness) is judged after the run in gate and prune.

Each moves before the first child process — the manifest and the
coverage policy read with the corpus, the caller's words validated at
parse, the record-only problems judged from spec and store — and the
gate/verify CLI commands then share the witness run's resolver child
instead of opening a second.

Lands: cross-tool train chunk 155 (gofresh
docs/plans/cross-tool-train.md), its prepared-capture change sets.
