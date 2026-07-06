# Slice frontiers are silent about reflection, build tags, and init effects

Lands: when a corpus relies on slice completeness for automated context
assembly over code using reflection, build tags, or init-effect
registration.

REQ-go-slice returns the typed transitive dependency frontier — facts
only. Go code can depend on symbols the type graph never references:
reflection, `init` side effects, blank imports, plugin-style
registration, build-tag and GOOS/GOARCH file selection, test-only
imports. A consumer treating the slice as complete (context assembly for
an agent, targeted analysis) silently misses those edges.

The honest extension is not a bigger frontier but a *confidence
surface*: the precise typed frontier, plus named uncertainty expansions
("this package uses reflect/blank imports/build tags — the frontier may
be larger"), so no consumer can mistake minimal for exhaustive. Facts
only, per the clause — the expansion markers are themselves resolvable
from the code.
