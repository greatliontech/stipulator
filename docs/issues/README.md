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
- **[runtime-input-digest-races-the-run](runtime-input-digest-races-the-run.md)** — the testlog
  manifest is hashed after the run, so a fixture edited while its readers execute can pin
  pre-edit outcomes under a post-edit digest. *Lands: when gofresh can atomically bind
  observed runtime-input values to the reads that produced the outcome (atomic value
  observation), or when witness records are next redesigned.*
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
- **[go-module-rename-lacks-symbol-migration](go-module-rename-lacks-symbol-migration.md)** — a
  Go module-path change invalidates large stored-symbol sets, with no validated bulk retarget
  command or actionable remediation. *Lands: before a corpus with stored Go symbol references
  changes module path, or when the binding rewrite surface next changes.*
- **[go-subprocess-tree-ownership](go-subprocess-tree-ownership.md)** — Unix witness runs own
  their process groups, but package loading owns an opaque launcher and non-Unix tree termination
  cannot yet prove descendant cleanup on platform-facility failure. *Lands: when Go package loading
  execution is next redesigned, or before descendant-cancellation guarantees are claimed for
  non-Unix platforms.*
- **[witness-e2e-reds-only-under-gate](witness-e2e-reds-only-under-gate.md)** — a freshness
  witness runs red only inside completed whole-corpus gate runs and never in isolation; standing
  hypothesis is environmental degradation under whole-suite load, with the isolated diagnostic and
  disposition rule recorded. *Lands: when witness execution can apply the accepted test policy and
  the isolated diagnostic has been executed and dispositioned.*
- **[prune-check-blind-to-witness-resolved-gaps](prune-check-blind-to-witness-resolved-gaps.md)** — the
  unwitnessed prune lint suppresses witness evidence, so gaps resolved by executed witnesses are
  structurally undetectable by CI's check step. *Lands: when the unified check evaluates gap
  resolution inside its witnessed single pass, or when the prune lint gains a witnessed mode.*
- **[symlinked-member-escapes-lexical-validation](symlinked-member-escapes-lexical-validation.md)** — an
  in-tree symlink pointing outside the tree passes the lexical escape checks in workspace member
  and policy module-root validation. *Lands: when validation resolves symlinks or execution refuses
  escaping resolved members.*
