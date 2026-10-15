# Conditional trust separation for heuristic provers

Lands: tooling-redesign 9.4

Activation condition: a concrete heuristic analyzer is proposed as a prover.
The census preserves that trigger rather than implementing speculative trust
tiers now.

REQ-evidence-ladder's analyzer-proof rung and REQ-go-witness-class's
recognized structural-library gate are implemented by `internal/coverage`
and `internal/backends/golang/golang.go`. The current recognized structural
assertions are intended to entail their stated properties; recognition of
that library is not a license for a future heuristic to inherit its rank.

Before admitting such a prover, state its claim, soundness boundary and
evidence class, and enforce that distinction in classification and coverage.
A likely-fault or suspicious-pattern result cannot silently become analyzer
proof. Pin the proposed heuristic beside the existing structural assertions
and preserve the latter's authority; do not assume an exact new rank without
the concrete analyzer's contract.
