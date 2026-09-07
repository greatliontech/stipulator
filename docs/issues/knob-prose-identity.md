# Knob prose has no identity pin between the guidance document and the wire

REQ-mcp-guidance makes `docs/guidance.md`'s knob prose the authoritative
superset and calls a schema or flag usage string that contradicts it a
defect, and the coverage tests pin tool descriptions by identity — but
knob prose is pinned by name only: `TestGuidanceCoversTheWireSurface`
and `TestGuidanceCoversTheCLISurface` compare property and flag NAMES,
never their text, so a schema tag or usage string can drift from the
document unnoticed (the `no_test` purpose was re-synchronised by hand
across five strings).

Resolution: the schema tags and usage strings rendered FROM the
document's knob text (the terse wire detail as its first clause), or
pinned as a verbatim prefix of it by the coverage tests — one
mechanism, no hand copies.

Lands: cross-tool train chunk 169