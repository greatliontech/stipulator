# The proof tier assumes provers are sound; the first heuristic prover breaks that

Lands: when a heuristic (non-sound) analyzer prover is proposed.

The evidence ladder's top rung (REQ-evidence-ladder: analyzer proof)
outranks property witnesses because the current prover set —
`stipulate/structural`'s import constraints and interface satisfaction —
is near-sound: a passing assertion structurally entails the property.
The proof witness class is gated on that specific library
(REQ-go-witness-class), so today nothing weaker can claim the rung.

A future heuristic analyzer (suspicious-concurrency, likely-missing-
propagation classes) must not inherit it: a heuristic finding ranks below
a property witness, arguably below an example witness. Before admitting
one, the ladder needs a soundness tier on the prover — sound/near-sound
vs heuristic — with only the former granting the proof evidence class.
