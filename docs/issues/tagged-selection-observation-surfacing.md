# Tagged policy invocations lose stdlib observation admissions silently at the policy tier

A policy invocation declaring build tags runs its analyses under a
toolchain selection gofresh's two-axis audit fail-closes until that
selection's standard-library delta is walked and listed (gofresh
docs, chunk 126): standard-library observation admissions are
disabled for the tagged leg, so observation proofs strip and
serving degrades to execution. The posture is correct and the
gofresh notice is loud — but it surfaces mid-derivation, attributed
to gofresh, not at the tier where the operator authored the tags:
policy validation accepts the tagged invocation without comment,
and nothing ties the notice back to the specific invocation name.
Demonstrated 2026-08-30: the derive_publish fixture's dup-tagged
leg lost its shared-package publish under the gofresh
v0.88.0→v0.90.0 bump; a production policy with tags: ["integration"]
reaches the identical path.

Resolution shape: a stipulator-side notice at policy
load/validation naming the invocation and its unwalked selection
key (mirroring the audit key gofresh computes), so the cost is
visible where the tags are declared; the guidance decision map
already states the posture (landed with this filing).

Lands: with train chunk 112 (the MCP/guidance content audit — the
notice is exactly its refusal-carries-next-step doctrine applied to
the policy tier)
