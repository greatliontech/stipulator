# Issues

Deferred follow-ups. Each carries a `Lands:` trigger saying when it should be pulled in.

- **[proto-backend](proto-backend.md)** — descriptor-level verification via protocompile;
  spec exists, five requirements gapped. *Lands: when a corpus needs wire evidence that
  shape pins and Go witnesses cannot cover.*
- **[gopter-property-recognition](gopter-property-recognition.md)** — gopter-driven tests
  classify as example witnesses; the classifier recognizes fuzz targets and rapid drivers
  only. *Lands: when a corpus standardized on gopter needs invariant coverage.*
- **[out-of-process-backends](out-of-process-backends.md)** — trusted backend surfaces can move
  behind a wire protocol while Stipulator continues deriving evidence in the current run;
  mutation findings remain gomutant-owned. *Lands: when a second language backend is planned.*
- **[advisory-context-attachments](advisory-context-attachments.md)** — binding surfaces have
  no generic channel for producer-owned material in context dossiers; current consumers read
  producer-owned typed results directly. *Lands: when a consumer needs producer-owned material
  rendered in Stipulator context dossiers instead of consuming the producer's typed output
  directly.*
- **[slice-frontier-uncertainty](slice-frontier-uncertainty.md)** — typed frontiers miss
  reflection, build tags, and init effects; pew's closure model (sound floor, provably-safe
  refinement, resolve/widen/unverifiable dispositions) is the reference shape. *Lands: when a
  corpus relies on slice completeness for automated context assembly over such code.*
- **[prover-trust-tiers](prover-trust-tiers.md)** — the proof rung assumes near-sound provers;
  a heuristic analyzer must not inherit it. *Lands: when a heuristic analyzer prover is
  proposed.*
- **[witness-subset-adequacy](witness-subset-adequacy.md)** — a binding surface's union mutation
  oracle cannot say whether each requirement's own witnesses have teeth; an opt-in
  per-requirement probe is measurable without attribution claims. *Lands: when a requirement's
  risk profile demands per-requirement witness adequacy.*

- **[performance-evidence-axis](performance-evidence-axis.md)** — no clause kind or evidence
  class measures performance; pew recordings (guard-derived validity) are the binding-pin
  model applied to measurements and slot in without bending the trust model. *Lands: when a
  corpus declares a performance requirement.*
- **[term-matcher-ascii-boundaries](term-matcher-ascii-boundaries.md)** — `\b` is ASCII-only,
  so non-ASCII term names may never match a use site (silently missing uses-term edges); the
  lint mirrors the same semantics deliberately — fix both together on rune boundaries. *Lands:
  when a corpus declares non-ASCII term names.*

- **[go-subprocess-tree-ownership](go-subprocess-tree-ownership.md)** — Unix Go backend work
  (witness runs, normalization, discovery, and symbol loading via the owned resolver child)
  owns its process groups with descendant termination proven, but Windows descendant
  termination cannot be proven without a Windows host. *Lands: when Windows descendant
  termination is proven with a real spawned child (Windows host unavailable here).*
- **[closure-edit-revert-inside-run-span](closure-edit-revert-inside-run-span.md)** — a source
  edit and its exact revert both landing inside one package's capture-compile-run span restore
  the recorded closure hash over outcomes a transiently-edited binary produced; the
  runtime-input half of this family is narrowed by observation brackets (a
  content-and-metadata-exact restore within the span stays the shared residual), while closure
  fingerprints hash content alone. *Lands: when witness fingerprints gain pre-run-evaluation
  support binding closure content to the compile that consumed it, or when witness records are
  next redesigned.*

- **[mcp-progress-not-observed](mcp-progress-not-observed.md)** — suite-running MCP tools
  surfaced no progress to a live agent client; every call was backgrounded at the client's
  timeout. *Lands: when the harness MCP server is next restarted against a live agent
  client (emission audit landed; the live token observation remains, and the same
  restart validates that the client renders structuredContent-only payloads).*
- **[witness-store-gc](witness-store-gc.md)** — departed identities' witness variants
  accumulate without bound (eviction fires only on same-identity installs); cost-only,
  measured at 30 MB across two heavy-development corpora — below any actionable bar.
  *Lands: when store size or load-time validation first becomes a measured cost on a
  real corpus.*
- **[partitions-uncapped-seam-unpinned](partitions-uncapped-seam-unpinned.md)** — the
  partitions export form's `ProtoUncapped()` call is unpinned at the tool seam (a capped
  swap needs a disproportionate 12-component fixture); carries the CLI prune call-site
  residual of the same class. *Lands: when an MCP fixture exceeding OverlapCap becomes
  proportionate, when the partitions tool seam next changes, or (prune residual) when
  prune's CLI seam next changes.*
- **[scope-prefix-boundary-semantics](scope-prefix-boundary-semantics.md)** — view scoping
  is raw-prefix (`example.com/p` keeps `example.com/p2`; `docs/spec` matches `docs/specs.md`)
  across docs, symbols, and diagnostics alike, and Path-empty scopes drop a build-broken
  package's diagnostic while keeping its Broken row; over-inclusion/visibility only, verdict
  unaffected. *Lands: when scope matching semantics are next deliberately changed.*
- **[structural-call-absence-verb](structural-call-absence-verb.md)** — "never constructs
  X" structural clauses have no verb: NoImport is transitive (stdlib forbiddance fails
  through any real dependency) and the shape verbs state presence, not capability absence;
  a direct-call-absence verb (structural.NoCall) would carry them. *Lands: when a
  structural requirement next needs a call-absence proof and the signature/import verbs
  demonstrably cannot carry it.*
