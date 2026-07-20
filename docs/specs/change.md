# Change model

There is one loop: the spec moves, coverage degrades, code follows until the
report is green. Refactoring without a spec delta is the degenerate case
handled entirely by binding verification. What needs first-class support is
the transition semantics — evidence inheritance across edits is authorial
intent, not derivable from hashes — and the window where the spec is
deliberately ahead of the code.

## Diff

**REQ-change-diff** (behavior): The `diff` operation MUST compare two
compiled IRs and report, per identity: added, removed, text-changed (content
hash delta), kind-changed (clause kind is marker metadata, invisible to the
content hash), metadata-only (location), and edge changes — text-changed and
kind-changed are independent axes, reported together when both change, and a
pure file reorganization reports no semantic delta.

**REQ-change-diff-revision** (behavior): The `diff` operation MUST accept a
git revision as the old corpus, compiling it from the repository's object
store at the corpus root's repo-relative path — no checkout, no worktree
mutation — so the committed contract and the working tree compare in one
invocation.

## Impact preview

**REQ-change-impact** (behavior): The `impact` operation MUST report, for
the working tree's whole change set against HEAD, the requirements the
change plausibly touches — those whose spec documents changed, resolved
through the diff semantics to the identities whose content moved, and
those bound to symbols resolving into changed source files — together
with the expected witness subjects whose packages the change set reaches
through the import graph, all without executing any test, loading no more
than symbol resolution requires, and never claiming a freshness verdict:
the preview names candidates for the witnessed surfaces to decide, and
its omissions are bounded by what symbol resolution and import reach can
see — a runtime-input edit reaches witnesses no import edge names, so an
empty preview is advisory, never a proof of no impact. Version-control
access stays confined to the adapter (REQ-core-vcs-free); a tree outside
any repository reports that plainly instead of guessing.

## Dispositions

Dispositions are operations, not records: they rewrite the stored bindings,
gaps, and tombstones in the same commit as the spec edit, and git holds the
history. Whether an editorial re-pin was honest is a review question — the
tool guarantees consistency, not semantics.

**REQ-change-default-normative** (behavior): A content-hash change without a
disposition MUST leave every binding pinned to the prior hash stale; breakage
is the default assumption.

**REQ-change-editorial** (behavior): An editorial disposition MUST re-pin the
identity's bindings to the new content hash without invalidation.

**REQ-change-split-merge** (behavior): Split and merge dispositions MUST
tombstone the source identities, verify that every successor declares a
`supersedes` edge to its sources — edges are spec-owned, authored in the
successor's metadata, never written by the tool — and retarget existing
bindings to the successors with their content pins cleared, which reads as
stale by contract.

**REQ-change-retire** (behavior): A retire disposition MUST tombstone the
identity and delete its bindings and gap records.

**REQ-change-transient** (invariant): Dispositions MUST NOT accrete a stored
log; their only persistent effect is the rewritten state of the
corpus-adjacent records and the tombstone registry.

**REQ-change-dangling** (invariant): A binding, gap, or attestation
record naming an identity not present in the corpus MUST be a
verification error.

**REQ-change-remediation** (behavior): Every rendered broken, stale, or
dangling finding whose repair the tool can compute MUST name that
repair operation in the finding itself, in the CLI's executable
spelling — the canonical surface; MCP tools mirror the verbs
one-to-one, so the verb name carries across — the stale content pin its
re-consent, the moved or unpinned shape its re-pin, the dangling record
its retraction or unbinding, the contradictory record pair both
retractions, the inadmissible attestation the evidence its cell
demands. A finding whose repair is the operator's own judgment (a red
witness, an unresolved symbol) names no operation: the tool computes
remediations, never prescribes fixes, and a wrong spelling misleads
worse than silence.

**REQ-record-cas** (invariant): Every record write MUST carry the
content the computing operation read for its target file — absence
included — with the applier refusing the whole batch when any target
moved in between, naming the moved file: last-writer-wins would
silently drop a concurrent agent's records, and identity or approval
metadata in the records is not the answer — identity stays with the
transport, never in the store. A batch checks every precondition
before its first write and stages every file before its first rename,
so a concurrent write refuses cleanly within a process — one apply at
a time — and a mid-batch fault leaves at most a partial state the
working tree makes visible, never a silent mix. Across processes the
precondition is best-effort: with lock files banned, the moments
between check and rename stay open, and git remains the serialization
point of record. The precondition is transient, in memory, dying with
the operation — no stored version counters, no lock files.

## Gaps

**REQ-gap-record** (behavior): A gap MUST be a committed textproto record
under `.stipulator/gaps/` naming exactly one requirement identifier, a
reason, and a landing condition.

**REQ-gap-verb** (behavior): Gap records MUST be writable through a tool
operation that validates the requirement identifier against the compiled
corpus and requires a reason and a landing condition at write time,
updating an existing declaration in place — a changed landing condition
is surfaced, never silently retargeted — and refusing to overwrite a
record that names a different requirement — with a manual condition's
fired bit expressible through the operation, and re-declaring a record
whose manual condition text is unchanged preserving its fired state,
the preservation surfaced when it overrides an unfired
declaration: an unfire is a lifecycle retarget, so it happens only
through an explicit changed declaration, never as a side effect of
re-declaring.

**REQ-gap-bulk** (behavior): The declaring operation MUST accept many
requirements in one call sharing one reason and landing condition,
validating all-or-nothing so a typo mid-list declares nothing, together
with a self landing sentinel resolving to each named requirement's own
coverage — the design-stage idiom where every declared requirement
lands on itself.

**REQ-gap-retract** (behavior): A gap record MUST be retractable through
a tool operation that deletes the record without touching the tombstone
registry — retraction withdraws a declaration about a requirement, never
the requirement itself — working equally on a dangling record whose
requirement has already left the corpus: the dangling state is what
retraction repairs, so corpus validation cannot gate it. Retracting a
requirement that has no gap record is an error, not a no-op; many
retractions in one call validate all-or-nothing, so a typo mid-list
retracts nothing; and verification's dangling-gap error names this
retraction as its repair.

**REQ-gap-conditions** (behavior): A landing condition MUST be either
machine-evaluable — `covered(<id>)`, `exists(<id>)` — or manual, firing
only when explicitly marked fired: an external judgment distinct from
attestation evidence.

**REQ-gap-lifecycle** (behavior): Verification MUST classify each gap as
`open`, `due` (its landing condition holds), or `resolved` (its requirement
is covered — and, for a gap with a manual landing condition, the condition
has also been explicitly fired: a manual condition is an external judgment
coverage cannot make, so a covered requirement with an unfired manual gap
stays `open`, a declared violation that outlives green witnesses). A gap
on a requirement the active policy renders exempt is `resolved` when its
landing condition holds — explicitly fired, for a manual condition:
coverage is not a state an exempt cell can reach, so the condition alone
defines completion, and without this arm the record would have no
reachable terminal state.

**REQ-gap-resolved-pruned** (behavior): The `prune` operation MUST delete
resolved gap records — a resolved gap is satisfied, dead record weight.
Detecting resolution is the coverage evaluation `gate` already performs,
so `gate` surfaces the count of resolved gaps awaiting prune,
discoverable from a run already made; the gate never deletes records
itself. `prune --check` reports a resolved gap
that lingers without deleting anything. The resolved-record evaluation
takes its witness evidence from the serving class — proven-fresh records
with selective execution of the stale remainder (REQ-core-one-execution)
— never a whole policy execution demanded for pruning alone.

**REQ-gap-prune-dangling** (behavior): An explicit dangling mode of
`prune` — with a check form — MUST delete dangling gap records in bulk,
judged against the compiled corpus alone with no witness evidence and no
symbol resolution, while the ordinary resolved-record prune never
deletes a dangling record: danglingness is a corpus-and-records fact,
resolution is a coverage judgment, and conflating them would let record
cleanup masquerade as satisfied work.

## The gate

The gate needs no before/after comparison: a red requirement is either
declared (a gap names it) or a violation. Pre-existing reds need declarations
exactly like new ones, which is what makes the migration window auditable.

**REQ-gate-no-undeclared** (behavior): The gate MUST fail exactly when some
requirement is `uncovered`, `stale`, or `broken` and no gap record names it.

## The unified check

One command answers "does this tree pass": it compiles the corpus,
obtains witness evidence — served from proven-fresh records with
witness-only selective execution of the stale remainder by default, or
from one whole execution of the accepted test policy when the caller
demands suite judgment — verifies bindings against that evidence,
evaluates coverage and gaps, and renders one verdict. Suite health is
claimed only by the full form, where health and witness evidence come
from the same execution per REQ-core-one-execution, so a passing suite
is never discarded and re-derived and a witness failure occurs inside
the run whose health the gate judged. The default form is the warm
loop's verdict: it re-runs exactly what moved and claims no health.

**REQ-check-verdict** (behavior): The unified check MUST derive its one
verdict from a single evaluation pass — compilation, witness evidence,
binding verification, coverage, gap evaluation, and prune residue —
never composing the answer from subprocess invocations of the individual
operations. By default the witness evidence comes from freshness-served
records plus witness-only selective execution of the stale remainder — a
witness-evidence invocation demanding no suite-health disposition
(REQ-core-one-execution) — and the verdict fails exactly when
compilation fails, the accepted test policy record is missing or invalid
(REQ-policy-explicit), verification reports problems,
REQ-gate-no-undeclared fails, or prune residue remains. A caller
demanding suite judgment selects full execution: the policy executes
whole, health derives from that same execution, and the verdict
additionally fails when suite health is unhealthy. The result names
which evidence class produced it, so a witness-evidence verdict is never
mistaken for a health-judged one. A cancelled check yields no verdict at
all — cancellation is an operational abort, never a pass or a fail.

**REQ-check-diagnostics** (behavior): A failing check MUST surface the
retained output of every failing policy invocation and every failed or
degraded witness, naming a degraded execution distinctly from an
assertion failure — a red witness whose output is discarded leaves an
environment-induced failure and a real regression indistinguishable, so
retained failure output is part of the verdict, not a courtesy.

**REQ-gate-change-signature** (behavior): The verification report SHOULD
classify the change signature per requirement, with the record pins as
the baseline — no verification outcome is ever persisted, so "changed"
means "moved against a pin": a requirement whose proof-shape pins moved
or whose proof failed while its behavior witnesses stayed green — at
least one, all green; a witness-less requirement attests nothing — is
labeled a rearchitecture (structure moved under an intact behavior
contract); a behavior witness failing while the requirement's content
pin is current — red with no corresponding spec delta — is flagged as
semantic drift (behavior diverged under a stable contract).
