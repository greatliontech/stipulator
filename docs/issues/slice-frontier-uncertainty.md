# Slice frontiers are silent about reflection, build tags, and init effects

Lands: when a corpus relies on slice completeness for automated context
assembly over code using reflection, build tags, or init-effect
registration.

REQ-go-slice returns the typed transitive dependency frontier — facts
only. Go code can depend on symbols the type graph never references:
reflection, `init` side effects, blank imports, plugin-style
registration, build-tag and GOOS/GOARCH file selection, test-only
imports. A consumer treating the slice as complete silently misses those
edges.

The solved reference model is pew's closure analysis
(`github.com/thegrumpylion/pew`, docs/spec.md §7): a sound
package-level floor (Tier 1, over-approximates, never false-complete), a
declaration-level refinement that shrinks only where provably safe
(Tier 2, SSA/RTA), and a blind-spot taxonomy where every unseen path
gets exactly one disposition — resolvable (add the precise edge),
bounded-but-unresolved (widen to the sound floor, never guess), or
external dependence (an honest `unverifiable` verdict, not a hash).
Adopting that shape gives slice consumers soundness by construction
instead of uncertainty annotations they must interpret.
