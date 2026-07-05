# Context facts

Work dissemination needs code context, and assembling it must cost no
tokens: the tool excerpts deterministically what no agent should transcribe.
Everything here returns facts — never selections, budgets, or rendering
policy, which belong to consumers.

**REQ-context-seeds** (behavior): For a requirement set, stipulator MUST
derive seed symbols as the bindings of the set's closure — the requested
requirements and their spec neighborhood — so greenfield work, whose own
requirements are unbound, seeds from its bound neighbors.

**REQ-context-partitions** (behavior): Stipulator MUST compute candidate
work partitions over a requirement set as a derived report — connected
components of intersecting closures, each carrying its seeds and the
packages its code slice touches, with pairwise package overlaps reported —
leaving selection and ordering entirely to the caller.
