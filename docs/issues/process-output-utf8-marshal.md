# Raw process output can carry invalid UTF-8 into proto string fields

Pre-existing (reviewer-demonstrated during the runner-environment
inspectability work, 2026-09-02): test binaries emit bytes, not text —
a latin-1 path, a locale-encoded name, binary garbage in a panic dump —
and those bytes flow verbatim into `FailureDiagnostic.Output` and the
MCP response digests, which are edition-2023 proto string fields whose
UTF-8 validation refuses the whole message at marshal. A verification
run whose diagnostic carries one invalid byte fails to serialize its
result: an operational fault standing in for a verdict.

The cut-point half of the hazard is fixed: every fixed-offset
truncation the diagnostics own (`boundedBuffer.write`'s cap cut, the
environment report's value and report caps, the enrichment room cut)
now backs off to a rune boundary, and the environment report scrubs its
own content. What remains is the flow-through of intact invalid bytes
from process output — reachable with no truncation at all.

The fix needs a design choice: scrub at the ingest boundary
(`boundedBuffer.write` with a cross-chunk rune carry, since a rune
legitimately split across two write calls must not be scrubbed), scrub
once at the diagnostic-construction boundary (`SetOutput` call sites),
or move the fields to `bytes`. The mcpserver `truncate` helper's string
cut is the same class on the response-digest side.

Lands: first field-observed marshal failure on a diagnostic or check
result, or with the next executor-diagnostics change set.
