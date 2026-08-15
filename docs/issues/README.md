# Issues

Deferred follow-ups. Each carries a `Lands:` trigger saying when it should be pulled in.

- **[corpus-residue-post-summary-diet](corpus-residue-post-summary-diet.md)** — the five
  spec commits after the last pin update left the whole-tree check red: eight
  requirements' content pins await re-consent, the summary diet's deleted histogram
  tests still have bindings, and several new tests register coverage unbacked. *Lands: cross-tool train chunk 42.*
- **[goos-race-build-dimensions](goos-race-build-dimensions.md)** — GOOS/GOARCH and the
  implicit race tag are invisible to both discovery and resolution; tag-set views cover
  only the tags dimension. *Lands: cross-tool train chunk 90.*
- **[multiply-selected-witness-identity](multiply-selected-witness-identity.md)** — on the
  tugboat corpus every invocation selects `./...`, every witness is multiply-selected,
  and records carry no per-invocation identity: 927/927 uncacheable, the serving path
  never engages, every chunk-gate check re-executes the world. *Lands: cross-tool train chunk 46.*
- **[cerebro-uncacheable-mass-measured](cerebro-uncacheable-mass-measured.md)** — 2,407
  uncacheable witnesses make the cerebro check re-execute everything (~22 min vs a ~2-min
  floor family); reason classes quantified, fixes owned by gofresh's bracket/classifier
  items plus the shared-view fix here. *Lands: cross-tool train chunk 16.*
- **[publication-ladder-collapse](publication-ladder-collapse.md)** — publishGroup and
  publishExecuted are near-duplicate publication ladders; collapsing them onto one would
  also fold the serving path's two per-group closing validations into one and drop the
  retry's redundant pre-publish validate. *Lands: cross-tool train chunk 43.*
- **[proto-backend](proto-backend.md)** — descriptor-level verification via protocompile;
  spec exists, five requirements gapped. *Lands: when a corpus needs wire evidence that
  shape pins and Go witnesses cannot cover.*
- **[gopter-property-recognition](gopter-property-recognition.md)** — gopter-driven tests
  classify as example witnesses; the classifier recognizes fuzz targets and rapid drivers
  only. *Lands: cross-tool train chunk 42.*
- **[out-of-process-backends](out-of-process-backends.md)** — trusted backend surfaces can move
  behind a wire protocol while Stipulator continues deriving evidence in the current run;
  mutation findings remain gomutant-owned. *Lands: when a second language backend is planned.*
- **[slice-frontier-uncertainty](slice-frontier-uncertainty.md)** — typed frontiers miss
  reflection, build tags, and init effects; pew's closure model (sound floor, provably-safe
  refinement, resolve/widen/unverifiable dispositions) is the reference shape. *Lands: cross-tool train chunk 45.*
- **[prover-trust-tiers](prover-trust-tiers.md)** — the proof rung assumes near-sound provers;
  a heuristic analyzer must not inherit it. *Lands: when a heuristic analyzer prover is
  proposed.*

- **[performance-evidence-axis](performance-evidence-axis.md)** — no clause kind or evidence
  class measures performance; pew recordings (guard-derived validity) are the binding-pin
  model applied to measurements and slot in without bending the trust model. *Lands: when a
  corpus declares a performance requirement.*
- **[term-matcher-ascii-boundaries](term-matcher-ascii-boundaries.md)** — `\b` is ASCII-only,
  so non-ASCII term names may never match a use site (silently missing uses-term edges); the
  lint mirrors the same semantics deliberately — fix both together on rune boundaries. *Lands: cross-tool train chunk 42.*

- **[closure-edit-revert-inside-run-span](closure-edit-revert-inside-run-span.md)** — a source
  edit and its exact revert both landing inside one package's capture-compile-run span restore
  the recorded closure hash over outcomes a transiently-edited binary produced; the
  runtime-input half of this family is narrowed by observation brackets (a
  content-and-metadata-exact restore within the span stays the shared residual), while closure
  fingerprints hash content alone. *Lands: cross-tool train chunk 46.*

- **[mcp-progress-not-observed](mcp-progress-not-observed.md)** — suite-running MCP tools
  surfaced no progress to a live agent client; every call was backgrounded at the client's
  timeout. *Lands: cross-tool train chunk 41.*
- **[witness-store-gc](witness-store-gc.md)** — departed identities' witness variants
  accumulate without bound (eviction fires only on same-identity installs); cost-only,
  measured at 30 MB across two heavy-development corpora — below any actionable bar.
  *Lands: cross-tool train chunk 42.*
- **[partitions-uncapped-seam-unpinned](partitions-uncapped-seam-unpinned.md)** — the
  partitions export form's `ProtoUncapped()` call is unpinned at the tool seam (a capped
  swap needs a disproportionate 12-component fixture); carries the CLI prune call-site
  residual of the same class. *Lands: cross-tool train chunk 42.*
- **[scope-prefix-boundary-semantics](scope-prefix-boundary-semantics.md)** — view scoping
  is raw-prefix (`example.com/p` keeps `example.com/p2`; `docs/spec` matches `docs/specs.md`)
  across docs, symbols, and diagnostics alike, and Path-empty scopes drop a build-broken
  package's diagnostic while keeping its Broken row; over-inclusion/visibility only, verdict
  unaffected. *Lands: cross-tool train chunk 42.*
- **[structural-call-absence-verb](structural-call-absence-verb.md)** — "never constructs
  X" structural clauses have no verb: NoImport is transitive (stdlib forbiddance fails
  through any real dependency) and the shape verbs state presence, not capability absence;
  a direct-call-absence verb (structural.NoCall) would carry them. *Lands: when a
  structural requirement next needs a call-absence proof and the signature/import verbs
  demonstrably cannot carry it.*
