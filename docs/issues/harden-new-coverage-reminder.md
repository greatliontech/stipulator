# verification does not remind when new coverage lacks hardening

Lands: when gate/verify output is used to drive the full adversarial loop for a
change set.

`verify` and `gate` can show that new requirements became covered and their
witnesses pass. They do not currently say whether the newly covered surface has
also been mutation-tested in the current change set. As a result, a user can add
bindings, get a clean gate, and still need out-of-band memory to run harden or
document why harden cannot reach the new surface.

Add a reminder surface keyed to new or changed requirement bindings: list covered
requirements whose implementing symbols or witness sets changed since the base
commit and have no matching fresh harden sheet. The output should distinguish
"harden can run this" from "no harden target; see staged-delta scope report" so
it does not turn coverage into a false gate.
