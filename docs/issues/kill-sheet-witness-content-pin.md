# Kill sheets pin the witness set by symbol, not content — strengthened tests leave stale sheets

Lands: when the hardening record's pin set is next extended.

A hardening record pins the mutated symbol's body hash, the witness set it ran against, the
operator-set version, and the mutant budget — but the witness set is pinned by symbol name
only. Strengthening a bound witness (adding an assertion that would now kill a recorded
survivor) does not invalidate the sheet: `harden` serves the cached record, and the survivor
report is stale — it names a mutant the current tests demonstrably kill.

Observed in a consuming corpus: a bound construction test was strengthened with a non-empty
assertion that kills a `zero return` survivor; the cached sheet kept reporting the survivor
until the witness *set* changed (an additional test was bound), which forced a new pin and
re-measurement — a workaround, not a mechanism.

Fix: pin witness content the same way binding pins clause content — a content or body hash
per witness symbol in the sheet's pin set, so a witness edit invalidates exactly the sheets
it ran in. Sibling concern to the landed toolchain pin (REQ-harden-records names the
toolchain among the sheet's pins), which covers inputs outside the tree; this one is an
input inside the tree that the pin set still misses.
