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
content hash), metadata-only (location), and edge changes — such that a pure
file reorganization reports no semantic delta.

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
tombstone the source identities, create `supersedes` edges from the
successors, and retarget existing bindings to the successors in stale state.

**REQ-change-retire** (behavior): A retire disposition MUST tombstone the
identity and delete its bindings and gap records.

**REQ-change-transient** (invariant): Dispositions MUST NOT accrete a stored
log; their only persistent effect is the rewritten state of the
corpus-adjacent records and the tombstone registry.

**REQ-change-dangling** (invariant): A binding or gap record naming an
identity not present in the corpus MUST be a verification error.

## Gaps

**REQ-gap-record** (behavior): A gap MUST be a committed textproto record
under `.stipulator/gaps/` naming exactly one requirement identifier, a
reason, and a landing condition.

**REQ-gap-conditions** (behavior): A landing condition MUST be either
machine-evaluable — `covered(<id>)`, `exists(<id>)` — or attested, firing
only when explicitly marked fired.

**REQ-gap-lifecycle** (behavior): Verification MUST classify each gap as
`open`, `due` (its landing condition holds), or `resolved` (its requirement
is covered).

**REQ-gap-resolved-pruned** (behavior): The `fmt` operation MUST delete
resolved gap records, and lint reports a resolved gap that lingers.

## The gate

The gate needs no before/after comparison: a red requirement is either
declared (a gap names it) or a violation. Pre-existing reds need declarations
exactly like new ones, which is what makes the migration window auditable.

**REQ-gate-no-undeclared** (behavior): The gate MUST fail exactly when some
requirement is `uncovered`, `stale`, or `broken` and no gap record names it.

**REQ-gate-change-signature** (behavior): The verification report SHOULD
classify the change signature — labeling a change whose structural proofs
changed state while behavior witnesses stayed green as a rearchitecture, and
flagging a behavior witness turning red without a corresponding spec delta as
semantic drift.
