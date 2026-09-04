# A renamed test leaves its spec enforcement pointer dangling

Spec clauses name their enforcing tests ("Enforced by `TestX`") — the
one sanctioned spec-to-code cross-reference — and `stipulator retarget`
rewrites binding symbols after a rename, but not the pointer in the
spec prose, so a renamed test keeps its binding and loses its pointer
silently. Nothing checks that every pointer names a bound symbol.

Resolution: the pointer judged against the store — every "Enforced by"
name resolves to a tests- or proves-role binding of that requirement —
by a document-level walk, and `retarget` rewriting pointers alongside
binding symbols.

Lands: user decision
