# The executed-reason and resolution-notice faces carry unclassed reasons

The uncacheable face's reasons are typed (`internal/backends/golang/reasons.go`:
a reason is minted over a listed class and explains by the class's kind). Two
other reason vocabularies the backend renders are bare strings with no class
and no explain kind:

- the executed-reason account (`TestRun.ExecutedReasons`, why a stale subject
  holding prior evidence re-executed): `witnessrun.go`'s
  "post-run served-record revalidation faulted: …" and "mid-run drift: …",
  beside the serving refusals and the source-failure reason it shares with the
  uncacheable face;
- the resolution notices, rendered as "resolution typed: <symbol>: <reason>":
  two spellings composed in `served.go` ("no current capture", "no longer
  declared in the selected source") beside gofresh's closure-moved verdict
  reasons passed through bare — a face's table would classify a pass-through
  by its source, as the uncacheable face's culprit classes do, not own its
  text.

REQ-mcp-explain governs uncacheable reasons alone, so neither face is a
conformance fault today; an agent reading an executed reason or a resolution
notice has no explain answer for it, and a composer of the spellings this
package owns there can spell what it likes. The collapse: each face's vocabulary a table of its own (a class with a
rendering and, where explain should answer it, a kind), the three faces one
shape; whether explain answers the two faces is a clause question for the
spec.

Lands: cross-tool train chunk 249 (the witness pipeline's monoliths — runWitnesses holds both accounts).
