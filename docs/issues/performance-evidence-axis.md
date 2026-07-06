# No clause kind or evidence class exists for performance requirements

Lands: when a corpus declares a performance requirement.

The clause kinds (behavior, invariant, wire, structural) and the
evidence ladder have no home for "MUST complete within …" or "MUST NOT
regress …": no witness class measures, and no policy row could demand
measurement.

pew (`github.com/thegrumpylion/pew`) is structurally the missing
evidence backend already: a benchmark recording is a persisted *claim*
whose validity is derived at verification time from the current tree
through guards (closure ∧ runtime-inputs ∧ toolchain ∧ machine ∧
buildconfig) — the binding-pin model exactly, so it composes with
REQ-core-claims-untrusted rather than bending it. `pew status` is the
verifier; `valid` means the measurement is proven reusable against the
current tree; `stale`/`unverifiable` grants nothing and reads red.

The integration shape: a `performance` clause kind; a policy row whose
minimum evidence is a guard-valid recording bound to the requirement;
the recording store as another record home; verdicts folding into the
coverage buckets (valid → covered, stale → stale, unverifiable →
broken-or-gapped). Comparison thresholds (regression tolerances) are
spec content — they belong in the clause text, not the tool.
