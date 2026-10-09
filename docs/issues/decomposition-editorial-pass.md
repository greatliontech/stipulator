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
- From the witness-freshness split: dangling antecedents in
  -carve-out ("the proof", "the compartment pin"), -vouches ("the same
  review discipline"), -exemptions ("Three classes are exempt" — from
  what), -runtime-bounds ("likewise not identity-bearing", leaning on
  -record-identity), -scratch-namespaces ("as an exclusion does");
  sentences byte preservation stranded in the wrong paragraph —
  -scratch-namespaces' closing exclusions rule belongs in -inputs,
  -capture-groups' "Exclusions, vouches, and the purity assertion
  partition capture groups but never the record identity" beside
  -record-identity's subject, -carve-out's fail-closed converse
  ("Anything else short of valid … absence of proof never serves an
  outcome") is the base's "exactly when" converse (its witness
  TestLoadUnreadableIsEmpty registers both meanwhile), -concurrency's
  parent and resolver soft-ceiling sentence is not about witness
  concurrency; several obligations under one keyword, each with
  distinct witnesses — -seeded (the direct never-serve rule; the
  transitive walk; unclassifiable subjects refused; a classification
  fault degrades), -concurrency (the spawn bound; the memory term; the
  parent's soft ceiling; inner width; one environment; width capture
  groups), -inputs (its MUST governs the reviewed-exclusions rule while
  the lead sentence's build-input capture is witnessed by five of its
  ten bindings), -exemptions (the three classes; the temp root; machine
  facts), -runtime-bounds (identity; health; store garbage
  collection), -revalidation (post-run revalidation; the memoization
  statement); a permission keyword governing mandatory constraints —
  -isolation's MAY over "the re-runs belong to the package's unit,
  complete before its records install, inside the package's own slot"
  and -purity's MAY over "Stipulator selects that proof only when …
  exactly one selected top-level runnable", each pinned by its
  witnesses and each wanting a MUST sentence of its own; idioms —
  -seeded's "MUST never serve and never publish" and -runtime-bounds'
  "MUST re-address no record" (a consequence elevated; the premise is
  "is not identity-bearing") read as "MUST NOT"; a prose mention
  without its id — -cache-format-ledger's "the witness-freshness
  carve-out's diff base" (cite -carve-out); bindings on the base pinning
  a rule no sentence of the family states —
  TestSelectingInvocationAnswersNothingForAnAbsentPackage and
  TestSubjectsOfOrdersByPackageThenSymbol (a helper's absent-package
  lookup; the subject ordering),
  TestExecutedRecordDropsWhenItsInputMovesBeforeItsPublish and
  TestGoDeriveRuntimeDriftAndUnverifiableSkipRecords (an executed record
  whose input moved before its publish is dropped and counted
  uncacheable — the nearest sentence -cache-format-install's "its
  closing validation passed").
- The coverage clause: state that a split under a MAY base raises its
  sub-ids' evidence bar — REQ-evidence-witness-freshness and
  REQ-evidence-resolution-freshness were MAY bases any static evidence
  satisfied, and their MUST sub-ids each need an executed witness now;
  the clause says nothing of a split's effect on the bar.

Lands: cross-tool train chunk 248 (stipulator's spec-corrections chunk,
which edits these documents; its plan entry carries the rider).
