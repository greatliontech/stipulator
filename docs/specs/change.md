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

## Gaps

**REQ-gap-record** (behavior): A gap MUST be a committed textproto record
under `.stipulator/gaps/` naming exactly one requirement identifier, a
reason, and a landing condition.

**REQ-gap-verb** (behavior): Gap records MUST be writable through a tool
operation that validates the requirement identifier against the compiled
corpus and requires a reason and a landing condition at write time,
updating an existing declaration in place — a changed landing condition
is surfaced, never silently retargeted — and refusing to overwrite a
record that names a different requirement.

**REQ-gap-conditions** (behavior): A landing condition MUST be either
machine-evaluable — `covered(<id>)`, `exists(<id>)` — or manual, firing
only when explicitly marked fired: an external judgment distinct from
attestation evidence.

**REQ-gap-lifecycle** (behavior): Verification MUST classify each gap as
`open`, `due` (its landing condition holds), or `resolved` (its requirement
is covered).

**REQ-gap-resolved-pruned** (behavior): The `prune` operation MUST delete
resolved gap records — a gap whose requirement has reached the covered
bucket is satisfied, dead record weight. Detecting resolution is the
coverage evaluation `gate` already performs, so `gate` surfaces the count
of resolved gaps awaiting prune, discoverable from a run already made; the
gate never deletes records itself. `prune --check` reports a resolved gap
that lingers without deleting anything.

## The gate

The gate needs no before/after comparison: a red requirement is either
declared (a gap names it) or a violation. Pre-existing reds need declarations
exactly like new ones, which is what makes the migration window auditable.

**REQ-gate-no-undeclared** (behavior): The gate MUST fail exactly when some
requirement is `uncovered`, `stale`, or `broken` and no gap record names it.

## The unified check

One command answers "does this tree pass": it compiles the corpus,
executes the accepted test policy once, verifies bindings against that
execution, evaluates coverage and gaps, and renders one verdict. Suite
health and witness evidence come from the same execution per
REQ-core-one-execution, so a passing suite is never discarded and
re-derived, and a witness failure occurs inside the same run whose health
the gate judged — not in a second run with different conditions.

**REQ-check-verdict** (behavior): The unified check MUST derive its one
verdict from a single evaluation pass — compilation, one execution of the
accepted test policy, binding verification, coverage, gap evaluation, and
prune residue — failing exactly when compilation fails, the accepted test
policy record is missing or invalid (REQ-policy-explicit: without it there
is no execution whose suite health the verdict could judge), verification
reports problems, suite health is unhealthy, REQ-gate-no-undeclared fails,
or prune residue remains, and never composing the answer from subprocess
invocations of the individual operations. A cancelled check yields no
verdict at all — cancellation is an operational abort, never a pass or a
fail.

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
