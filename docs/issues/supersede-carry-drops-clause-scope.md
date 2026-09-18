# A supersede carries a clause-scoped claim as a whole-requirement claim

`internal/author/dispose.go`'s supersede carry builds each successor
binding from the source's requirement, backend, symbol, role, and shape
pin — never its clause scope — so a claim granted to one clause of the
superseded requirement lands on the successor granting every clause,
wider than what was declared; `bindingExists` (the carry's duplicate
guard) keys without the clause for the same reason. REQ-change-split-
merge says nothing about the clause: whether a carried claim should keep
its scope (a successor declaring the same label), drop it deliberately
(the successor's clauses are its own), or refuse to carry a scoped claim
at all is a semantic call over what evidence a supersede may widen.

Lands: user decision.
