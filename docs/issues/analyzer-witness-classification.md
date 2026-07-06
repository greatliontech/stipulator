# verify reports analyzer witnesses as WITNESS_CLASS_UNSPECIFIED

Lands: when the witness classifier or the verify surface is next touched.

A test whose body invokes `stipulate/structural.NoImport` is recognized by the gate as the
analyzer proof a structural clause demands — the requirement buckets COVERED with no
reasons. But `verify` reports the very same binding row with
`witnessClass: WITNESS_CLASS_UNSPECIFIED`.

Two surfaces disagree about one binding's evidence class. A consumer auditing conformance
from `verify` output concludes the structural requirement still lacks its prover shape;
the gate says it is covered. Only one of them can be right, and today the classification
logic that the gate uses evidently does not feed the class field verify emits.

Repro: bind a test calling `structural.NoImport` (role `tests`) to a structural clause;
run `verify` and `gate`; compare the binding row's `witnessClass` against the
requirement's bucket.

Fix: one classifier, both surfaces — verify's binding rows should carry the same evidence
class the gate's bucketing derived (an analyzer-proof class alongside
example/property/executed), so the per-binding view and the per-requirement verdict can
never drift.
