# Two publication ladders and a validate pair for one spec concept

`publishGroup` (derive.go) and `publishExecuted` (witnessrun.go) are
near-duplicate publication ladders — eligibility, proof attach,
final-fingerprint assembly, post-run check, closing validation, record
assembly — two code mechanisms for the spec's one publication concept.
Collapsing them onto one ladder would also enable two observation-pass
savings the duplication currently blocks:

- The serving path validates each group twice per executed run —
  finishGroup's close (gating serves) and publishExecuted's close
  (gating publication). One closing validation per group could gate
  both if the served-verdict consumption moved after the publication
  close.
- The retry path validates its fresh view before publishExecuted and
  again inside it; the pre-publish validate closes no checks and the
  publish close refuses the same drift with better per-subject reasons.

The layered closes are also load-bearing for review reasoning: the
publish-close's outcome-equivalence attestation (its refusal is
shadowed by finishGroup's close on the main path) depends on the pair
staying together — a collapse must re-derive which close gates what
and re-anchor the source-mover discard net accordingly.

Lands: cross-tool train chunk 43.
