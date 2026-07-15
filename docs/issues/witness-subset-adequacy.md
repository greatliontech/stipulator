# Union mutation oracles cannot establish each requirement's witness adequacy

Lands: when a requirement's risk profile demands per-requirement witness
adequacy beyond a binding surface's union oracle.

A binding surface groups one implementation symbol with every requirement it
implements and every `tests` or `proves` binding associated with those
requirements. A mutation adapter can map its executable test bindings into one
union oracle so the implementation body is measured once. A kill then proves
some vouching test constrains the body, not that the witnesses credited to each
requirement do: one requirement's witness can be vacuous while another's tests
keep every mutant dead.

A per-requirement adequacy probe is measurable without any attribution
fiction: run the symbol's mutants against a single requirement's
witnesses only, answering "would this requirement's own witnesses notice
this body breaking at all?" It re-admits the cross-requirement survivor
noise the union removed, so it remains an opt-in caller policy rather than the
default target mapping. Gomutant owns the resulting findings; Stipulator does
not consume them as evidence or gate input.
