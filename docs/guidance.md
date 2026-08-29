# stipulator — tool-resident guidance

## verbs

### compile
**does:** Compile the spec corpus; returns diagnostics (empty means clean) and counts.
**knobs:**
- `ir` (cli) — print the compiled IR as textproto.
**when:** use compile alone while authoring spec documents; every other
verb recompiles for itself, so a clean compile is a precondition
check, never a required first step.
**example:** compile after editing a spec document to see diagnostics
before binding against the new text.

### verify
**does:** Check records against the corpus and code.
**knobs:**
- `no_test` (mcp, cli as `no-test`) — skip running tests (no witnesses).
- `view` (mcp) — summary (default: hygiene and witness counts with change signatures) or bindings (the per-binding rows).
- `ids` (mcp) — comma-separated requirement identifiers to scope binding rows to; unknown identifiers refuse.
- `filter` (mcp) — requirement-id glob over binding rows.
- `path` (mcp) — prefix over declaring document or symbol.
**when:** use verify for binding hygiene and witness detail; prefer
check for the one-verdict pass, and gate when the question is
coverage buckets rather than binding health.
**example:** verify with view=bindings and ids=REQ-model-graph to read
one requirement's binding rows.

### gate
**does:** Coverage gate: buckets and the gate verdict.
**knobs:**
- `view` (mcp, cli) — summary (default: pass/fail + counts + violations), reds (red requirements with reasons), or full (every requirement).
- `ids` (mcp, cli as `req`) — requirement identifiers to scope to (comma-separated on mcp; repeatable on the cli); unknown identifiers refuse on the mcp surface.
- `bucket` (mcp, cli) — scope to one bucket: uncovered, stale, broken, covered, exempt, attested.
- `filter` (mcp, cli) — requirement-id glob, e.g. REQ-arch-*.
- `path` (mcp, cli) — prefix over declaring spec document or bound symbols.
- `json` (cli) — machine output: the selected view as JSON.
- `quiet` (cli) — exit code only.
**when:** use gate when the question is per-requirement coverage —
which bucket, which reds, and whether the gate passes; prefer check
for the unified pass with witness evidence and gap evaluation. Gate
runs the test suite, and it is the one view verb with no test
opt-out.
**example:** gate with view=reds to list the red requirements and
their reasons.

### check
**does:** One pass, one verdict: does this tree pass.
**knobs:**
- `full` (mcp, cli) — execute the whole accepted policy and judge suite health; default serves fresh witnesses and executes only the stale remainder.
- `view` (mcp) — summary (default: verdict, counts, capped red rows, top-blocker rows, diagnostic headings) or full (the whole CheckResult with per-test maps and retained output).
- `ids` (mcp, cli) — comma-separated requirement identifiers scoping the pass itself: fresh witnesses still serve whole-tree, only stale subjects bound to them execute, the verdict is flagged partial (scope_partial) with scope-boundary reds excluded, and unknown identifiers refuse; incompatible with full.
- `json` (cli) — machine output: the check result as deterministic JSON.
- `quiet` (cli) — exit code only.
**when:** use check as the default verdict surface — warm calls are
cheap because fresh witnesses serve and only the stale remainder
executes; use full=true when suite health must be judged, which only
whole execution can do. The pass also reports prune residue. It
fails exactly when compilation fails, the accepted policy record is
missing or invalid, verification reports problems, a red
requirement has no gap naming it, or a resolved gap record lingers
unpruned; full additionally fails on unhealthy suite health. A tree
failing the check is a successful call carrying passed=false.
**example:** check before entering review; check with
ids=REQ-go-static-binding while iterating on one requirement's fix.

### bind
**does:** Author validated binding claims; pins applied immediately.
**knobs:**
- `requirement` (mcp, cli as `req`) — requirement identifier (single-claim form).
- `symbol` (mcp, cli) — backend-scoped symbol reference (single-claim form).
- `role` (mcp, cli) — implements, tests, or proves (single-claim form).
- `backend` (mcp, cli) — language backend (default go).
- `file` (mcp, cli) — target binding file (derived when empty).
- `claims` (mcp) — batch claims validated all-or-nothing — a failure anywhere authors nothing; alternative to the single-claim fields.
**when:** use bind after the requirement exists and the symbol
resolves; the requirement must exist, generated files are rejected,
and errors explain what to fix. The mcp batch form validates
all-or-nothing.
**example:** bind req=REQ-guidance-coverage role=tests
symbol=module/pkg.TestCoverage to claim a test enforces a clause.

### unbind
**does:** Remove binding claims for a requirement, optionally narrowed by symbol and role.
**knobs:**
- `requirement` (mcp, cli as `req`) — requirement identifier.
- `symbol` (mcp, cli) — narrow to one symbol.
- `role` (mcp, cli) — narrow to one role.
**when:** use unbind when a claim is wrong or its symbol was renamed
(bind the successor after); matching nothing is an error, never a
silent no-op.
**example:** unbind req=REQ-x symbol=module/pkg.TestOld before
binding the renamed test.

### gap
**does:** Declare, fire, retract, or list coverage gaps.
**knobs:**
- `requirement` (mcp, cli as `req`) — requirement identifiers (comma-separated on mcp; repeatable on the cli; all share the reason and landing condition).
- `reason` (mcp, cli) — why the gap exists (required unless retracting or firing).
- `covered` (mcp, cli) — lands when this requirement is covered (self = each requirement's own coverage).
- `exists` (mcp, cli) — lands when this requirement exists.
- `manual` (mcp, cli) — lands on this externally judged condition, fired explicitly.
- `fired` (mcp, cli) — mark the manual condition fired (without manual: fire the existing gaps).
- `retract` (mcp, cli) — delete the gap records instead of declaring (dangling records included; retraction never touches the tombstone registry).
- `excuses` (mcp, cli) — violation classes the gap excuses, from uncovered|stale|broken (comma-separated on mcp; repeatable on the cli; default uncovered alone).
- `list` (mcp, cli) — list every gap record with its declaration fields and evaluated state (open|due|resolved|dangling) — the read surface, witness evidence gathering only for the gap-relevant requirements; editing a gap is re-declaring it.
**when:** use gap to record a known coverage hole with the condition
that lands it — never to silence a red without a reason; batches
apply all-or-nothing, and list is the read surface.
**example:** gap req=REQ-new-clause reason="enforcement lands with
the consumer leg" manual="the consumer binding lands".

### attest_requirement
**surfaces:** mcp, cli as attest requirement
**does:** Record the weakest evidence: a reason-carrying voucher for a requirement.
**knobs:**
- `requirement` (mcp, cli as `req`) — requirement identifier.
- `reason` (mcp, cli) — why the requirement is judged satisfied (required unless retracting).
- `retract` (mcp, cli) — withdraw the requirement's judgment instead of authoring one.
**when:** use attestation only where the policy admits it as a cell's
minimum — it renders as its own bucket, never covered, and re-stales
when the requirement's text moves; prefer a binding whenever a test
or analyzer can carry the claim.
**example:** attest requirement req=REQ-ops-runbook reason="judged by
operations review 2026-08".

### pin
**does:** Backfill unset content pins and refresh shape pins; named requirements editorially re-pin.
**knobs:**
- `ids` (mcp, cli as `req`) — requirement identifiers to editorially re-pin (comma-separated on mcp; repeatable on the cli); empty backfills unset pins.
**when:** run the blanket form after any spec edit — a differing
content pin is never rewritten by it, so staleness cannot be
laundered; the response names requirements awaiting re-consent, and
naming them is the editorial re-consent. The blanket form is also
what re-pins a moved shape, and it names the symbols whose differing
shape pins it rewrote; naming requirements re-consents clause text
only, and that form reports any shape mismatch it is not going to
fix instead of claiming quiescence. Never silent: no-ops say so.
**example:** pin, read the awaiting-re-consent list, then pin
req=REQ-x for each requirement whose new text you consent to.

### dispose
**surfaces:** mcp
**does:** Apply a spec-change disposition: editorial, retire, or supersede.
**knobs:**
- `kind` — editorial (re-pin after meaning-preserving edit), retire (tombstone a removed identity), or supersede (tombstone sources, retarget bindings to declaring successors).
- `requirement` — target for editorial/retire.
- `from` — comma-separated sources for supersede.
- `into` — comma-separated successors for supersede.
- `force` — retire even when no record names the identity.
**when:** use dispose when spec text changed shape — the cli spells
the same dispositions as three subcommands (dispose editorial,
dispose retire, dispose supersede).
**example:** dispose kind=supersede from=REQ-old into=REQ-a,REQ-b
after splitting a clause.

### dispose editorial
**surfaces:** cli
**does:** Re-pin a requirement's bindings after a meaning-preserving edit.
**knobs:**
- `req` — requirement identifier.
**when:** use after an edit that changes wording, not meaning; the
mcp surface spells this dispose with kind=editorial.
**example:** dispose editorial --req REQ-x after a typo fix.

### dispose retire
**surfaces:** cli
**does:** Tombstone an identity removed from the spec; delete its records.
**knobs:**
- `id` — retired identity (requirement id or term name).
- `force` — retire even when no record names the identity.
**when:** use when a requirement or term left the spec for good; the
mcp surface spells this dispose with kind=retire.
**example:** dispose retire --id REQ-obsolete.

### dispose supersede
**surfaces:** cli
**does:** Tombstone sources and retarget their bindings to declaring successors.
**knobs:**
- `from` — comma-separated source identifiers (removed from the spec).
- `into` — comma-separated successor identifiers (declaring supersedes).
**when:** use for splits and merges (the aliases); the mcp surface
spells this dispose with kind=supersede.
**example:** dispose supersede --from REQ-old --into REQ-a,REQ-b.

### retarget
**does:** Rewrite stored binding symbols under an exact prefix mapping (module-rename repair).
**knobs:**
- `backend` (mcp, cli) — backend whose symbols retarget (default go).
- `from` (mcp, cli) — old symbol prefix (module path).
- `to` (mcp, cli) — new symbol prefix.
- `check` (mcp, cli) — report affected identities without writing.
**when:** use after a module rename; the prefix matches at a path
or member boundary, and all-or-nothing — replacements must resolve,
collisions refuse the batch, shape pins re-derive and content pins
ride unchanged. Run a check preview first when sibling modules share a
dotted prefix: a member dot and a dotted path element are lexically
ambiguous, so example.com/mod captures example.com/mod.v2 symbols.
**example:** retarget from=example.com/old to=example.com/new
as a check preview, read it, then run for real.

### prune
**does:** Delete resolved gap records; dangling and store modes repair orphans.
**knobs:**
- `check` (mcp, cli) — lint: non-zero exit when records linger, deleting nothing.
- `dangling` (mcp, cli) — delete gap records naming requirements no longer in the corpus (the bulk repair; corpus and records only, no tests).
- `store` (mcp, cli) — garbage-collect this corpus's witness store: drop record variants whose identity is absent from the current obligation universe (departed, renamed, or unbound tests) plus unreadable entries; explicit only — an identity absent here may be live on another branch; composes with no other mode.
- `no-test` (cli) — skip the witness run (resolved-gap pruning may under-detect).
**when:** use prune when check or gate advertises resolved-record
residue — resolved means the requirement is covered and any manual
landing condition was explicitly fired: satisfied dead weight;
witness evidence gathers only for the gapped requirements, so
deletion is cheap on a warm tree. Writes only under
.stipulator/gaps/ (store mode under the witness store).
**example:** a check preview to lint for lingering records at a
chunk close.

### context
**surfaces:** mcp
**does:** Per-requirement dossier: clause text, coverage, gaps, attestations, bindings, closure seeds.
**knobs:**
- `ids` — comma-separated requirement identifiers.
- `slice` — include the code-slice declaration frontier (the expensive leg).
- `no_test` — skip running tests (no witnesses); dossiers render from records alone.
- `export_path` — write the dossier report to this path under .stipulator/exports/ and return only its location — the budget valve for many-id calls.
**when:** use context to orient on requirements before writing code —
facts only, selection is yours; prefer read_spec when only the spec
text is needed.
**example:** context ids=REQ-a,REQ-b with slice=true before designing
a fix.

### partitions
**surfaces:** mcp
**does:** Candidate work partitions: closure-connected components with seeds and overlaps.
**knobs:**
- `ids` — comma-separated requirement identifiers; empty means all red requirements.
- `no_test` — skip running tests (no witnesses); partitions derive from records alone.
- `export_path` — write the full report (uncapped overlaps) to this path under .stipulator/exports/ and return only its location.
**when:** use partitions to split red work into disjoint components —
disjoint components can fan out in parallel.
**example:** partitions with no ids to partition all red work.

### read_spec
**surfaces:** mcp
**does:** Read the self-contained bundle for requirement ids: requirements, closure, terms, context.
**knobs:**
- `ids` — comma-separated requirement identifiers.
**when:** use read_spec to read spec text without resource support —
it mirrors the bundle resource; prefer context when records and
coverage matter too.
**example:** read_spec ids=REQ-mcp-tools before touching the MCP
surface.

### explain
**surfaces:** mcp
**does:** Derivation chain for a dynamic-state refusal, from culprit to the innermost refusing expression.
**knobs:**
- `reason` — a witness's uncacheable reason to parse the culprit from.
- `package` — culprit package path (with symbol, overrides reason).
- `symbol` — culprit variable name.
**when:** use explain when a witness reports a dynamic-state
uncacheable reason — pass the reason verbatim, or name the package
and symbol; the chain derives against the policy-scoped views
verdicts use.
**example:** explain with the uncacheable reason string a check
result carried.

### diff
**surfaces:** cli
**does:** Per-identity IR delta between two trees, or against a git revision.
**knobs:**
- `against` — git revision holding the old corpus (HEAD~1, branch, tag, hash).
**when:** use diff to see what a spec edit changed semantically —
two roots compare checked-out trees; against reads the committed
corpus straight from the object store, no checkout.
**example:** diff --against HEAD~1 after a spec-editing commit.

### impact
**surfaces:** cli
**does:** Preview what the working-tree change set plausibly touches.
**knobs:** none
**when:** use impact for a cheap pre-check preview — it executes
nothing and claims no freshness verdict; an empty preview is
advisory, never proof of no impact, because reach through
non-import couplings is invisible here. The witnessed surfaces
(check, verify) decide.
**example:** impact after staging a change, before running check.

### policy init
**surfaces:** cli
**does:** Derive the universal-race test policy record when absent.
**knobs:** none
**when:** use once at adoption — it derives the policy equivalent to
the universal race suite witness execution assumes (one race-enabled
./... invocation per workspace member) and writes it to
.stipulator/policy.textproto only when no record exists; an existing
record is the reviewed contract: a matching one is a no-op, a
diverging one is an error, never a rewrite.
**example:** policy init in a fresh corpus, then review and commit
the record.

### init
**surfaces:** cli
**does:** Scaffold the manifest for a new corpus.
**knobs:** none
**when:** use init once, exactly where the corpus should live —
nested corpora are deliberate, so init scaffolds where invoked and
skips root discovery.
**example:** init, then write a spec document and run compile.

### mcp
**surfaces:** cli
**does:** Serve the corpus and operations over MCP (stdio).
**knobs:** none
**when:** use mcp as the server entry point for an MCP client;
outside a corpus the server still starts — corpus tools return the
teaching error per request, while guidance still serves its
embedded document.
**example:** mcp under an MCP client configuration.

### guidance
**does:** Serve this guidance: a verb's full section, or the decision map.
**knobs:**
- `verb` (mcp) — the verb to describe; empty serves the decision map.
**when:** use guidance to learn what a verb does, what a knob
controls, and when to use which — the tool answers from its own
embedded document, so served prose and repository documentation are
the same bytes; the cli takes the verb as its positional argument.
**example:** guidance verb=check; guidance with no verb for
orientation.

## decision map

stipulator verifies code against a compiled requirement corpus.
The loop: check answers "does this tree pass" — summary view by
default; it serves fresh witness evidence and executes only what
moved, so warm calls are cheap; full=true additionally judges suite
health. gate and verify give coverage and binding detail (summary
default; views and scopes opt-in). read_spec and context orient
before writing code; partitions splits red work into disjoint
components. explain answers a witness's dynamic-state uncacheable
reason with its derivation chain — pass the reason, or an explicit
package and symbol. Authoring: bind (claims batch, all-or-nothing),
gap (declare/fire/retract, batch), attest_requirement, pin (blanket
backfills unset pins only and names differing pins awaiting
re-consent; naming ids is the editorial re-consent that rewrites
them), dispose (editorial/retire/supersede), retarget (bulk
symbol-prefix rewrite after a module rename; check previews),
prune (resolved records; dangling=true repairs orphans). Long calls
(check/gate/verify/prune/context/partitions/gap list=true) report
phase progress when the request carries a progress token — send one
and be patient rather than assuming a hang; results state the phase
a deadline expired in. All writes stay under .stipulator/; spec
documents and source are never edited. A policy invocation
declaring build tags runs under a toolchain selection gofresh
fail-closes until that selection's standard-library delta is walked
and listed: standard-library observation admissions are disabled
for the tagged leg (a loud toolchain-unaudited notice names it), so
prefer untagged or race-only invocations unless the tag selection
has been walked. CLI-only: diff, impact,
policy init, init, and mcp itself; MCP-only: context, partitions,
read_spec, explain, and the dispose kind= form the cli spells as
three subcommands. The guidance verb serves any verb's full section
— knobs, when-to-use, example — from the tool's own embedded
document.
