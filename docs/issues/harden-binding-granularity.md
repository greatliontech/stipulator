# harden mutation is function-scoped, but requirements are finer than functions

Lands: when harden gains statement-level attribution, or the binding model documents the
union-of-tests expectation.

When several requirements bind the same implementation symbol (common: one function that
carries multiple concerns — e.g. a `Compile` that both fails closed on diagnostics *and* builds
cites *and* enforces image totality), `harden --req R` mutates the *whole* function body. Mutants
that live in another requirement's logic inside that function survive R's bound tests, so R
reports low kills even though R's own slice is well tested. The practical workaround is to bind
to R the union of every test that exercises the shared function — which inflates each
requirement's test set with tests that pin *other* requirements, blurring what R actually
proves.

Observed binding `internal/corpus.Compile` to `REQ-corpus-diagnostic-fatal`: 16/24 killed on
the first pass, the survivors all in the cite-building and deadline-loop statements (which
belong to `cite-sourced` / `image-total`, not `diagnostic-fatal`). Rose to 21/24 only after
binding the compile happy-path tests to `diagnostic-fatal` too.

Options: attribute mutation to the statements a requirement's binding covers (sub-function
granularity), or accept function granularity and document that a shared symbol expects the
union of its tests bound to each requirement — so the noise is understood, not mistaken for a
coverage hole.
