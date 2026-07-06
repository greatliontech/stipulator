# Union kill-sheets cannot say whether one requirement's own witnesses have teeth

Lands: when a requirement's risk profile demands per-requirement witness
adequacy beyond the union sheet.

REQ-harden-mutation deliberately mutates each symbol once against the
union of all its requirements' witnesses, disclaiming statement-level
attribution. A consequence: a kill proves *some* vouching test
constrains the body, not that the witnesses credited to a specific
requirement do — REQ-B's witness can be vacuous in practice while
REQ-A's tests keep every mutant dead.

A per-requirement adequacy probe is measurable without any attribution
fiction: run the symbol's mutants against a single requirement's
witnesses only, answering "would this requirement's own witnesses notice
this body breaking at all?" It re-admits the cross-requirement survivor
noise the union removed, so it is an opt-in probe (a harden flag), not a
sheet replacement, and its output is a finding surface, never gate
input (REQ-harden-exploration).
