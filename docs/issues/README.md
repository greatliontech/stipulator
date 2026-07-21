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

- **[go-module-rename-lacks-symbol-migration](go-module-rename-lacks-symbol-migration.md)** — a
  Go module-path change invalidates large stored-symbol sets, with no validated bulk retarget
  command or actionable remediation. *Lands: before a corpus with stored Go symbol references
  changes module path, or when the binding rewrite surface next changes.*
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

- **[standing-gap-absorbs-unrelated-red](standing-gap-absorbs-unrelated-red.md)** — a covered
  requirement carrying an open manual-condition gap can later go red for an unrelated reason
  and the gate raises no violation, while the gap's reason describes the original class —
  witnessed regressions still surface through suite health; the blind spot is auditability.
  *Lands: when gap records gain violation-class scoping, or when the gap lifecycle next
  changes.*

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
- **[impact-preview-omission-bounds](impact-preview-omission-bounds.md)** — two
  REQ-change-impact spec-amend candidates: worktree-only symbol resolution leaves pure
  deletion invisible code-side (spec-side deletions do report), and non-implemented
  backends are skipped with no user-visible statement. *Lands: when the user disposes
  the REQ-change-impact spec-amend candidates.*
- **[prune-serving-class-unpinned](prune-serving-class-unpinned.md)** — call-path identity
  choices without dedicated pins: prune's serving-class witness source, and the partitions
  export's uncapped form at the tool seam; a callee swap would survive the suite. *Lands:
  when the witness path gains an execution-class seam or observability hook a test can
  assert against.*
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
