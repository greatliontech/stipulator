# The decomposition's editorial residue

Chunk 270 decomposed the mega-requirements into sub-contract ids
byte-preservingly: each base keeps its lead sentence's paragraph, every
later sentence run becomes its own paragraph under a sub-id, and the
one change per new paragraph is one elevation to a normative keyword.
That mechanic cannot touch the residue it exposes, which an editorial
pass over the split documents owns (each a reviewed finding, recorded
in its change set's commit message):

- Dangling antecedents across a split: REQ-mcp-tools-messages and
  several mcp sub-paragraphs refer back ("then", "additionally", "The
  stream"); REQ-check-verdict-scoped opens "MUST select the scoped
  witness-evidence class instead" with the alternative in another
  paragraph; REQ-check-verdict-moot's "an earlier term" leans on the
  base's term list; REQ-go-owned-processes-telemetry-home's "outside
  that boundary" and "the one sanctioned escape from the boundary", and
  -runner's "carrying this boundary", name the base's boundary.
- A base reading as an exceptionless universal: REQ-go-owned-processes
  claims every descendant terminates on every platform, while the
  telemetry escape ("On platforms whose config home no variable
  selects …") lives only in -telemetry-home.
- Paragraphs holding several obligations under one keyword, each
  already with a distinct witness so a further split is clean:
  REQ-check-verdict-scoped (the narrowing with the degraded fallback;
  scope-blocked with the excused row; residue and resolved-gap serving;
  the partial flag and echo; the refusals — "MUST narrow / MUST be
  classed / MUST flag / MUST refuse"); REQ-go-owned-processes-runner
  (the children through the runner with the roots probe's memo; the
  witness stream read to its end with the stderr-truncation rule; a
  listing refused on the hold or a nonzero exit; the children's
  enumeration — "MUST read / MUST refuse"); REQ-mcp-tools-messages (a
  verbless message list beside the presence rule its keyword elevates).
- A duplication across sub-paragraphs: REQ-check-verdict-class-named's
  "nor a scoped verdict for a global one" restates -scoped's partial
  flag and echo; fold at -scoped's split.
- Prose contradicting a declared gap: -runner's children enumeration
  says the loader's `go list` children are "each swept with the
  operation that owns it", while the gap on REQ-go-owned-processes
  says the loader's descendants are not swept by the operation's
  boundary (a cancelled load ends its `go list`; the descendants are
  the limit) — state the gap's limit in the prose, at the latest when
  the gap's condition lands.
- Bindings pinning a rule no sentence of their family states, visible
  once the base is one paragraph: on REQ-go-owned-processes,
  TestResolverWireMappingsRoundTrip (wire enum round trips),
  TestResolverClientProtocolRoundTrips,
  TestResolverClientProtocolErrorSurfaces,
  TestWholeTreeForwardersSurfaceTheChildsFault and
  TestResolverClientLoadErrorPropagates (a dead or erroring child
  surfaces as an error, never a silent absence) — either an editorial
  clause for the resolver protocol's fault-surfacing rule, or the
  wire-mapping test retargeted to the clause it pins.
- From the evidence-five split: REQ-policy-attribution-no-merge reads
  "MUST never be merged" (one keyword to the lint; the idiomatic form is
  "MUST NOT be merged"); REQ-evidence-resolution-freshness-degrade is a
  one-sentence paragraph whose only cite is the near-homonym
  REQ-evidence-freshness-degrade (the witness path's rule) — a reader
  confuses the two ids; REQ-evidence-resolution-freshness-typed-load
  bundles three rules under one elevation (the typed load's scope; the
  serving and whole-tree backend forms; the record-writing guard — the
  fingerprint captured before and after must match), and its whole-tree
  and records-that-moved witnesses pin the second; REQ-evidence-
  witness-cache-format-ledger's elevated obligation ("MUST name that
  record's own test") is not the paragraph's main contract (stored
  once per compartment, installed atomically, reclaimed when
  unreferenced — what its three witnesses pin); -version states its
  obligation in its first clause and spends the paragraph on the bump
  policy, which wants its own; -record-keys holds the fingerprint key
  enumeration that -fingerprint and -strategy refer to ("the
  enumeration above") — move the fingerprint keys beside -fingerprint.

Lands: cross-tool train chunk 248 (stipulator's spec-corrections chunk,
which edits these documents; its plan entry carries the rider).
