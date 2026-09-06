# stipulator — tool-resident guidance

## verbs

### compile
**does:** Compile the spec corpus; returns diagnostics (empty means clean) and counts.
**knobs:**
- `ir` (cli) — print the compiled IR as textproto: an operator's inspection surface; the agent reads compiled requirements through the resources.
**when:** use compile alone while authoring spec documents; every other
verb recompiles for itself, so a clean compile is a precondition
check, never a required first step.
**example:** compile after editing a spec document to see diagnostics
before binding against the new text.

### verify
**does:** Check records against the corpus and code.
**knobs:**
- `no_test` (mcp, cli as `no-test`) — the records-only judgment: no witness run, no policy capture; bindings resolve and hygiene is judged for authoring flows that need only the binding rows, while the witnessed form serves fresh witnesses and is cheap once the store is warm.
- `view` (mcp, cli) — summary (default: hygiene and witness counts with change signatures) or bindings (one row per claim: requirement, role, clause, symbol, consent, resolution, outcome); the cli's summary is the operator's counts and broken rows, its bindings view the same rows as text. A scope narrows both: the summary's counts, signatures, and diagnostics are re-tallied over what the scope keeps, while the problems and the outside-policy count stay tree-wide.
- `ids` (mcp, cli as `req`) — requirement identifiers to scope the report to (comma-separated on mcp; repeatable on the cli); unknown identifiers refuse before any witness runs.
- `filter` (mcp, cli) — requirement-id glob to scope the report to.
- `path` (mcp, cli) — prefix over declaring document or bound symbol to scope the report to: "what claims this symbol" — the query to run before deleting or moving an exported symbol, so the answer is a view, never a grep over the record files.
- `json` (cli) — machine output for CI and scripts: the selected view as JSON.
**when:** use verify for binding hygiene and witness detail; prefer
check for the one-verdict pass, and gate when the question is
coverage buckets rather than binding health. Before deleting a
symbol, verify with view=bindings and path=<package.Symbol> (cli:
`--view bindings --path`) lists every claim on it.
**example:** verify with view=bindings and ids=REQ-model-graph to read
one requirement's binding rows; `stipulator verify --no-test --view
bindings --path example.com/kernel.Round` before removing `Round`.

### gate
**does:** Coverage gate: buckets and the gate verdict.
**knobs:**
- `view` (mcp, cli) — summary (default: pass/fail + counts + violations), reds (red requirements with reasons), or full (every requirement).
- `ids` (mcp, cli as `req`) — requirement identifiers to scope to (comma-separated on mcp; repeatable on the cli); unknown identifiers refuse before any witness runs.
- `bucket` (mcp, cli) — scope to one bucket: uncovered, partial, stale, broken, covered, exempt, attested.
- `filter` (mcp, cli) — requirement-id glob, e.g. REQ-arch-*.
- `path` (mcp, cli) — prefix over declaring spec document or bound symbols.
- `json` (cli) — machine output for CI and scripts: the selected view as JSON.
- `quiet` (cli) — exit code only, for CI.
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
- `view` (mcp) — summary (default: verdict, counts, capped red rows, top-blocker rows, diagnostic headings) or full (the whole CheckResult with per-test maps and retained output): the agent's token economy; the cli renders the red rows, the bounded histograms, and the counts, and `json` carries the whole result.
- `ids` (mcp, cli) — comma-separated requirement identifiers scoping the pass itself: fresh witnesses still serve whole-tree, only stale subjects bound to them execute, the verdict is flagged partial (scope_partial) with scope-boundary reds excluded, and unknown identifiers refuse; incompatible with full.
- `json` (cli) — machine output for CI and scripts: the check result as deterministic JSON.
- `quiet` (cli) — exit code only, for CI.
**when:** use check as the default verdict surface — warm calls are
cheap because fresh witnesses serve and only the stale remainder
executes; use full=true when suite health must be judged, which only
whole execution can do. The pass also reports prune residue. It
fails exactly when compilation fails, the accepted policy record is
missing or invalid, verification reports problems, an execution the
run performed came out red (a failed test or a degraded, build-failed,
or timed-out process — whatever the failing test is bound to; the
summary's witness_failure_headings name it), a red requirement has no
gap naming it, or a resolved gap record lingers unpruned; full
additionally fails on unhealthy suite health. Random-seeded property
witnesses (bodies directly driving rapid or gopter) never serve: they
execute on every check and read as uncacheable with that reason; a
witness the backend cannot classify at all (its package fails to load
under the invocation's selection) is refused serving the same way
under a reason naming the load gap. A tree
failing the check is a successful call carrying passed=false.
**example:** check before entering review; check with
ids=REQ-go-static-binding while iterating on one requirement's fix.

### bind
**does:** Author validated binding claims; pins applied immediately.
**knobs:**
- `requirement` (mcp, cli as `req`) — requirement identifier; on the cli each repetition starts a claim.
- `symbol` (mcp, cli) — backend-scoped symbol reference; on the cli exactly one per claim.
- `role` (mcp, cli) — implements, tests, or proves; on the cli once for all claims or one per claim.
- `backend` (mcp, cli) — language backend (default go); on the cli once for all claims or one per claim.
- `file` (mcp, cli) — target binding file (derived when empty); on the cli once for all claims or one per claim.
- `clause` (mcp, cli) — scope the claim to one payload clause of its requirement — the clause's ordinal (from 1, payload order) or the label a `**label**`-led list item declares; empty claims the whole requirement. A clause claim grants evidence to that clause alone, so a requirement whose remaining clauses hold no policy-meeting evidence reads `partial`, never covered; on the cli exactly one per claim when given at all (empty keeps that claim whole). An ordinal follows the item's position, counted from 1 whatever number the list displays: inserting an item above it retargets the claim, which the stale content pin surfaces and the named re-pin names — prefer labels where the spec declares them.
- `claims` (mcp) — batch claims validated all-or-nothing — a failure anywhere authors nothing; alternative to the single-claim fields — the agent's one-call authoring; the cli repeats its flags per claim.
**when:** use bind after the requirement exists and the symbol
resolves; the requirement must exist, generated files are rejected,
and errors explain what to fix. Both surfaces batch all-or-nothing:
repeated cli flag groups form claims exactly as the mcp claims list
does — values pair with claims by each flag's own occurrence order,
independent of how flags interleave on the command line — and a flag
count matching neither one nor the claim count is refused; a claim is
never silently dropped.
**example:** bind req=REQ-guidance-coverage role=tests
symbol=module/pkg.TestCoverage to claim a test enforces a requirement;
add clause=2 (or clause=label) to claim only its second payload clause.

### unbind
**does:** Remove binding claims for a requirement, optionally narrowed by symbol and role.
**knobs:**
- `requirement` (mcp, cli as `req`) — requirement identifier.
- `symbol` (mcp, cli) — narrow to one symbol.
- `role` (mcp, cli) — narrow to one role.
- `clause` (mcp, cli) — narrow to the claim scoped to this clause, as the claim spells it (ordinal or label); the way to remove one of two claims on a symbol, or a claim whose clause the corpus no longer declares.
**when:** use unbind when a claim is wrong or its symbol was renamed
(bind the successor after); its flags narrow one selection and form no
batch, so a repeated flag is refused rather than last-wins; matching
nothing is an error, never a silent no-op.
**example:** unbind req=REQ-x symbol=module/pkg.TestOld before
binding the renamed test.

### gap
**does:** Declare, fire, retract, or list coverage gaps.
**knobs:**
- `requirement` (mcp, cli as `req`) — requirement identifiers (comma-separated on mcp; repeatable on the cli; all share the reason and landing condition).
- `reason` (mcp, cli) — why the gap exists (required unless retracting or firing; one shared value — a repeated cli flag is refused, never last-wins).
- `covered` (mcp, cli) — lands when this requirement is covered (self = each requirement's own coverage; one shared value, repetition refused).
- `exists` (mcp, cli) — lands when this requirement exists (one shared value, repetition refused).
- `manual` (mcp, cli) — lands on this externally judged condition, fired explicitly (one shared value, repetition refused).
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
- `requirement` (mcp, cli as `req`) — requirement identifier (taken once; repetition refused).
- `reason` (mcp, cli) — why the requirement is judged satisfied (required unless retracting; taken once, repetition refused).
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
naming them is the editorial re-consent. One rewrite launders nothing
and the blanket form makes it: a content pin that differs while the
record's consent-source pin still matches consented to byte-identical
text (a tool rebuild moved the canonical form, not the spec) — such
records are current all along, the verification summary counts them
rehash-pending, and the blanket form rewrites them naming the
requirements as "rehashed (text unchanged)"; a current record with no
source pin gains one the same way. The blanket form is also
what re-pins a moved shape, and it names the symbols whose differing
shape pins it rewrote; naming requirements re-consents clause text
only, and that form reports any shape mismatch it is not going to
fix instead of claiming quiescence. Never silent: no-ops say so —
a named requirement whose text is unchanged answers "text unchanged;
nothing to re-consent", and a named re-pin over a rehashed record
names it so. A
named re-pin also names the clause each re-consented clause claim now
denotes — an ordinal follows its item's position, so read those lines
before trusting the re-pin — and refuses, writing nothing, when a
clause claim names a clause the edited text no longer declares (rebind
or unbind it first); on mcp every id is judged before the first write.
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
**when:** use dispose — the agent's one-call kind= form — when spec text changed shape; the cli spells
the same dispositions as three subcommands (dispose editorial,
dispose retire, dispose supersede). An editorial disposition's notes
name the clause each re-consented clause claim now denotes, and a
clause claim the new text no longer resolves refuses it. A supersede
runs on the corpus as edited — the sources already removed, the
successors declaring `supersedes` — in one step: the base corpus
need not compile (its refusal for the undeclared source names this
disposition), the tombstones are written, the edges accepted, and the
sources' bindings retargeted stale; a source no record names needs
force, the typo guard.
**example:** dispose kind=supersede from=REQ-old into=REQ-a,REQ-b
after splitting a clause.

### dispose editorial
**surfaces:** cli
**does:** Re-pin a requirement's bindings after a meaning-preserving edit.
**knobs:**
- `req` — requirement identifier (taken once; repetition refused).
**when:** use — the operator's shell spelling of dispose — after an edit that changes wording, not meaning; the
mcp surface spells this dispose with kind=editorial. The response
names the clause each re-consented clause claim now denotes, and a
clause claim the new text no longer resolves refuses the disposition.
**example:** dispose editorial --req REQ-x after a typo fix.

### dispose retire
**surfaces:** cli
**does:** Tombstone an identity removed from the spec; delete its records.
**knobs:**
- `id` — retired identity (requirement id or term name; taken once, repetition refused).
- `force` — retire even when no record names the identity.
**when:** use — the operator's shell spelling of dispose — when a requirement or term left the spec for good; the
mcp surface spells this dispose with kind=retire.
**example:** dispose retire --id REQ-obsolete.

### dispose supersede
**surfaces:** cli
**does:** Tombstone sources and retarget their bindings to declaring successors.
**knobs:**
- `from` — comma-separated source identifiers (removed from the spec); repeatable, every occurrence's identifiers join.
- `into` — comma-separated successor identifiers (declaring supersedes); repeatable, occurrences join.
- `force` — supersede a source no record names; the typo guard otherwise refuses.
**when:** use — the operator's shell spelling of dispose — for splits and merges (the aliases); the mcp surface
spells this dispose with kind=supersede. Run it on the corpus as
edited: the sources removed and the successors declaring
`supersedes` — the corpus need not compile first, the disposition
validates through the tombstones it writes. Name every source and
every successor of one split or merge in one call (the compile
refusal spells the whole component); a prose mention of a removed
source elsewhere is a dangling reference the same edit rewrites.
**example:** dispose supersede --from REQ-old --into REQ-a,REQ-b.

### retarget
**does:** Rewrite stored binding symbols under an exact prefix mapping (module-rename repair).
**knobs:**
- `backend` (mcp, cli) — backend whose symbols retarget (default go; taken once, repetition refused).
- `from` (mcp, cli) — old symbol prefix (module path; taken once, repetition refused).
- `to` (mcp, cli) — new symbol prefix (taken once, repetition refused).
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
- `no-test` (cli) — the records-only judgment: no witness run, no policy capture; resolved-gap pruning may under-detect without witness evidence, so the operator's records-only sweep is the reason it exists here.
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
- `no_test` — the records-only judgment: no witness run, no policy capture; dossiers render from records alone.
- `export_path` — write the dossier report to this path under .stipulator/exports/ and return only its location — the budget valve for many-id calls.
**when:** use context — the agent's orientation — to orient on requirements before writing code:
facts only, selection is yours; prefer read_spec when only the spec
text is needed.
**example:** context ids=REQ-a,REQ-b with slice=true before designing
a fix.

### partitions
**surfaces:** mcp
**does:** Candidate work partitions: closure-connected components with seeds and overlaps.
**knobs:**
- `ids` — comma-separated requirement identifiers; empty means all red requirements.
- `no_test` — the records-only judgment: no witness run, no policy capture; partitions derive from records alone.
- `export_path` — write the full report (uncapped overlaps) to this path under .stipulator/exports/ and return only its location.
**when:** use partitions — the agent's planning surface — to split red work into disjoint components;
disjoint components can fan out in parallel.
**example:** partitions with no ids to partition all red work.

### read_spec
**surfaces:** mcp
**does:** Read the self-contained bundle for requirement ids: requirements, closure, terms, context.
**knobs:**
- `ids` — comma-separated requirement identifiers.
**when:** use read_spec — the agent's orientation without resource support — to read spec text;
it mirrors the bundle resource; prefer context when records and
coverage matter too.
**example:** read_spec ids=REQ-mcp-tools before touching the MCP
surface.

### explain
**does:** Derivation chain for a dynamic-state refusal, from culprit to the innermost refusing expression.
**knobs:**
- `reason` (mcp, cli) — a witness's uncacheable reason to parse the culprit from.
- `package` (mcp, cli) — culprit package path (with symbol, overrides reason).
- `symbol` (mcp, cli) — culprit variable name.
**when:** use explain when a witness reports a dynamic-state
uncacheable reason — pass the reason verbatim, or name the package
and symbol; the chain derives against the policy-scoped views
verdicts use. The mcp returns the structured links; the cli prints
them one per line.
**example:** explain with the uncacheable reason string a check
result carried.

### diff
**surfaces:** cli
**does:** Per-identity IR delta between two trees, or against a git revision.
**knobs:**
- `against` — git revision holding the old corpus (HEAD~1, branch, tag, hash).
**when:** use diff — the operator's preview at the shell — to see what a spec edit changed semantically —
two roots compare checked-out trees; against reads the committed
corpus straight from the object store, no checkout.
**example:** diff --against HEAD~1 after a spec-editing commit.

### impact
**surfaces:** cli
**does:** Preview what the working-tree change set plausibly touches.
**knobs:** none
**when:** use impact — the operator's preview at the shell — for a cheap pre-check; it executes
nothing and claims no freshness verdict; an empty preview is
advisory, never proof of no impact, because reach through
non-import couplings is invisible here. The witnessed surfaces
(check, verify) decide.
**example:** impact after staging a change, before running check.

### policy init
**surfaces:** cli
**does:** Derive the universal-race test policy record when absent.
**knobs:** none
**when:** use once at adoption, as the operator's scaffolding: it derives the policy equivalent to
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
**when:** use init once — the operator's scaffolding — exactly where the corpus should live;
nested corpora are deliberate, so init scaffolds where invoked and
skips root discovery.
**example:** init, then write a spec document and run compile.

### mcp
**surfaces:** cli
**does:** Serve the corpus and operations over MCP (stdio).
**knobs:** none
**when:** use mcp as the server entry point for an MCP client — the operator's process, never the agent's;
outside a corpus the server still starts — corpus tools return the
teaching error per request, while guidance still serves its
embedded document.
**example:** mcp under an MCP client configuration.

### guidance
**does:** Serve this guidance: a verb's full section, or the decision map.
**knobs:**
- `verb` (mcp) — the verb to describe; empty serves the decision map — the agent's named parameter, where the cli takes the verb as its positional argument.
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
progress when the request carries a progress token — the phase,
per-invocation counts, and decision lines naming what each invocation
executes and why, and what persisted — send one and be patient rather
than assuming a hang; without a token the same lines reach the log
channel at info level; a deadline or cancellation names the phase it
ended in and what the run kept. At the CLI a failing verdict exits 1,
an interrupted run renders the same ending and then dies by the
signal that ended it (128 plus the signal's number where it cannot
be re-raised; a second signal ends the process at once), and an
operational fault exits 2. All writes stay under .stipulator/; spec
documents and source are never edited. A policy invocation
declaring build tags runs under a toolchain selection gofresh
fail-closes until that selection's standard-library delta is walked
and listed: standard-library observation admissions are disabled
for the tagged leg (a loud toolchain-unaudited notice names it), so
prefer untagged or race-only invocations unless the tag selection
has been walked. CLI-only: diff, impact,
policy init, init, and mcp itself; MCP-only: context, partitions,
read_spec, and the dispose kind= form the cli spells as
three subcommands. The guidance verb serves any verb's full section
— knobs, when-to-use, example — from the tool's own embedded
document.
