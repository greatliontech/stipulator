# Witness hardening

A passing test proves execution, not sensitivity: a witness is worth its
tier only if breaking the implementation breaks the test. Hardening makes
that mechanical at three costs — vacuity rejection at write time, targeted
mutation in the hot loop, and the exhaustive sweep as night-tier
exploration. Survivors are findings for judgment; the machine proves a
test can fail, never that it fails for the reason the spec means.

**body hash** (term): a backend-defined hash of a symbol's implementation
body; the version of behavior a hardening record vouches against — the
sibling of the shape hash, which covers only the declaration.

**REQ-harden-vacuity** (behavior): A `tests`-role binding whose bound test
contains no failure path — no failing testing call, no delegation to a
callee that receives a testing handle, and no panic — MUST be rejected at
write time, resolved from the code; reachability is deliberately not
decided here, that is what mutation is for.

**REQ-harden-mutation** (behavior): The `harden` operation MUST mutate the
bodies of a requirement's `implements` symbols and execute its bound tests
against each mutant in isolation — through build overlays, never by
touching the tree — reporting every surviving mutant as a finding with its
position and operator.

**REQ-harden-operators** (behavior): The mutation operator set MUST
comprise condition negation, comparison and logical-operator swaps,
statement deletion, and zero-value return substitution, applied
syntactically — a mutant that fails to compile or does not differ is
discarded, and a timed-out run counts as killed.

**REQ-harden-scope** (behavior): The `harden` operation MUST accept
requirement-set and changed-symbol scoping with a per-symbol mutant
budget, so the hot loop completes in seconds while the exhaustive sweep
remains available for the night tier.

**REQ-harden-records** (behavior): A hardening record MUST pin the mutated
symbol's body hash and carry the mutant count, the kill count, and each
survivor — a record whose body hash no longer matches the code is stale,
exactly as content and shape pins are.

**REQ-harden-exploration** (behavior): Hardening campaigns MUST NOT feed
the gate — survivors are findings awaiting disposition, a strengthened
test or an attested equivalence, never automatic failures.
