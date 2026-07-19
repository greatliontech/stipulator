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
- **[concurrent-record-writes](concurrent-record-writes.md)** — record verbs are last-writer-
  wins; concurrent agents need compare-and-swap at the verb layer, never actor metadata in
  records. *Lands: when concurrent agents write records in one working tree.*
- **[performance-evidence-axis](performance-evidence-axis.md)** — no clause kind or evidence
  class measures performance; pew recordings (guard-derived validity) are the binding-pin
  model applied to measurements and slot in without bending the trust model. *Lands: when a
  corpus declares a performance requirement.*
- **[term-matcher-ascii-boundaries](term-matcher-ascii-boundaries.md)** — `\b` is ASCII-only,
  so non-ASCII term names may never match a use site (silently missing uses-term edges); the
  lint mirrors the same semantics deliberately — fix both together on rune boundaries. *Lands:
  when a corpus declares non-ASCII term names.*
- **[mcp-root-failure-lacks-guidance](mcp-root-failure-lacks-guidance.md)** — MCP `compile`
  without a manifest returns the raw open error instead of the guided failure the CLI gives
  (`REQ-profile-root`: "failing with guidance"); an agent hitting it must guess between broken,
  misrooted, and uninitialized. *Lands: when the MCP server's root-discovery failure path next
  changes.*
- **[gap-bulk-cannot-express-self-landing](gap-bulk-cannot-express-self-landing.md)** — bulk
  `gap --req` shares one landing condition, so the design-stage idiom covered(self) needs one
  call per requirement; a `--covered self` sentinel would fix it. Observed: 22 gaps mis-targeted
  in a consuming corpus, retargeted individually (update-in-place worked as specified). *Lands:
  when the gap verb surface next changes.*
- **[mcp-gap-tool-single-requirement](mcp-gap-tool-single-requirement.md)** — the MCP `gap`
  tool takes one `requirement` where the CLI's `--req` is repeatable; agents declaring
  design-stage gaps pay one round-trip per requirement. *Lands: when the MCP `gap` input schema
  or gap operation semantics next change.*
- **[mcp-check-result-too-large](mcp-check-result-too-large.md)** — MCP `check` can return a
  full one-line result large enough for clients to truncate before the verdict is visible.
  *Lands: when the MCP `check` result schema or rendering next changes.*
- **[targets-empty-surface-lacks-guidance](targets-empty-surface-lacks-guidance.md)** — an empty
  binding-surface export does not explain that implementation bindings are missing. *Lands: when
  the binding-surface report diagnostics next change.*
- **[mcp-targets-artifact-handoff](mcp-targets-artifact-handoff.md)** — Stipulator surfaces feed
  gomutant, but MCP users must manually copy inline JSON between tool calls. *Lands: when the MCP
  `targets` export surface next changes, or when MCP clients can pass typed tool-result artifacts
  directly between tools.*
- **[mcp-bind-bulk-claims](mcp-bind-bulk-claims.md)** — initial corpus migrations need many
  validated binding claims, but MCP `bind` writes only one per call. *Lands: when the MCP `bind`
  input schema or binding record verb next changes.*
- **[go-module-rename-lacks-symbol-migration](go-module-rename-lacks-symbol-migration.md)** — a
  Go module-path change invalidates large stored-symbol sets, with no validated bulk retarget
  command or actionable remediation. *Lands: before a corpus with stored Go symbol references
  changes module path, or when the binding rewrite surface next changes.*
- **[go-subprocess-tree-ownership](go-subprocess-tree-ownership.md)** — Unix Go backend work
  (witness runs, normalization, discovery, and symbol loading via the owned resolver child)
  owns its process groups with descendant termination proven, but Windows descendant
  termination cannot be proven without a Windows host. *Lands: when Windows descendant
  termination is proven with a real spawned child (Windows host unavailable here).*
- **[symlinked-member-escapes-lexical-validation](symlinked-member-escapes-lexical-validation.md)** — an
  in-tree symlink pointing outside the tree passes the lexical escape checks in workspace member
  and policy module-root validation. *Lands: when validation resolves symlinks or execution refuses
  escaping resolved members.*
- **[closure-edit-revert-inside-run-span](closure-edit-revert-inside-run-span.md)** — a source
  edit and its exact revert both landing inside one package's capture-compile-run span restore
  the recorded closure hash over outcomes a transiently-edited binary produced; the
  runtime-input half of this family is narrowed by observation brackets (a
  content-and-metadata-exact restore within the span stays the shared residual), while closure
  fingerprints hash content alone. *Lands: when witness fingerprints gain pre-run-evaluation
  support binding closure content to the compile that consumed it, or when witness records are
  next redesigned.*
- **[gap-verb-cannot-fire-manual-condition](gap-verb-cannot-fire-manual-condition.md)** — the
  `fired` bit that resolves a manual-condition gap is unreachable from the gap verb: firing
  requires a direct record edit, and re-declaring a fired gap through the verb rewrites it
  unfired. *Lands: when the gap verb's input surface next changes, or when a manual-condition
  gap in this corpus first needs firing.*
- **[standing-gap-absorbs-unrelated-red](standing-gap-absorbs-unrelated-red.md)** — a covered
  requirement carrying an open manual-condition gap can later go red for an unrelated reason
  and the gate raises no violation, while the gap's reason describes the original class —
  witnessed regressions still surface through suite health; the blind spot is auditability.
  *Lands: when gap records gain violation-class scoping, or when the gap lifecycle next
  changes.*
- **[attestation-rejection-lacks-reason](attestation-rejection-lacks-reason.md)** — an
  attestation on a cell whose policy does not admit it renders as a bare violation with
  no reason naming the inadmissibility. *Lands: when the attested bucket's rendering next
  changes, or an agent session next burns a cycle on it.*
- **[gap-on-exempt-requirement-never-resolves](gap-on-exempt-requirement-never-resolves.md)** —
  a gap on an exempt (unbound MAY) requirement has no reachable terminal state. *Lands:
  when gap lifecycle evaluation next changes.*
- **[mcp-progress-not-observed](mcp-progress-not-observed.md)** — suite-running MCP tools
  surfaced no progress to a live agent client; every call was backgrounded at the client's
  timeout. *Lands: when REQ-mcp-progress is next verified against a real client.*
- **[witness-store-gc](witness-store-gc.md)** — departed identities' witness variants
  accumulate without bound (eviction fires only on same-identity installs); cost-only.
  *Lands: when the store next gains a maintenance surface, or when store size first
  becomes a measured cost.*
