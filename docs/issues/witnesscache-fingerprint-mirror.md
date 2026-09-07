# The witness cache mirrors gofresh's fingerprint field by field

`internal/witnesscache.Fingerprint` is a hand-maintained mirror of
`gofresh.Fingerprint` — fourteen fields, `FromGofresh`/`ToGofresh`
conversions, and a reflective round-trip guard test. Every field
gofresh adds is a red build here until the mirror grows it (the
v0.98.0 bump's `ClosureStrategy` was the second such addition after
`DynamicStateStrategy`), and the guard test skips under the fast tier,
so the red surfaces at the close-out tier, not at the bump.

The collapse: persist gofresh's own JSON form and keep only what the
cache adds — the guard-field split the custom `UnmarshalJSON`
enforces (machine and runtime guards never persisted; observation
proof and assertion never null) and the wire-key pin. A gofresh
field addition then rides the record without a mirror edit, and the
validity fields (the strategies) compare through gofresh's own
equality. Invariants preserved: REQ-evidence-witness-cache-format's
naming and content-agreement check; the guard-field exclusion; the
fail-closed reading of a record persisted before a strategy field.

Lands: the next gofresh fingerprint field addition, or the next change to the witness-cache record format.
