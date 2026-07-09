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

**REQ-harden-vacuity** (behavior): A `tests`- or `proves`-role binding
whose bound test contains no failure path — no failing testing call, no
delegation to a callee that receives a testing handle, and no panic —
MUST be rejected at write time, resolved from the code; reachability is
deliberately not decided here, that is what mutation is for.

**REQ-harden-mutation** (behavior): The `harden` operation MUST mutate
each targeted symbol's body and execute, against each mutant, the union
of the witness-role bound tests of every requirement that binds the
symbol as `implements` — in isolation, through build overlays, never by
touching the tree — reporting every surviving mutant as a finding with
its position and operator. A kill is attributed: it requires a named
failing test from that pinned witness set, a timeout, or a package-scope
failure — a break with no test-level event, admitted only when a
baseline probe of the unmutated tree passes, distinguishing a
goroutine-panic-class kill from environmental noise; a run that fails
any other way aborts the sweep without writing records, because a
corrupted measurement that reads as a sound one inflates kills in the
flattering direction. A
survivor means no test that vouches for the
body notices the breakage; statement-level requirement attribution is
deliberately not claimed, because no code-resolvable partition of a body
by requirement exists.

**REQ-harden-operators** (behavior): The mutation operator set MUST
comprise condition negation; comparison, logical, and arithmetic operator
swaps (including compound assignment and increment/decrement forms);
boolean-operand forcing; integer-literal increments; break/continue
swaps; statement deletion — dropping an assignment's store while keeping
its right-hand side evaluated, so removal-class mutants compile — and
zero-value return substitution, applied syntactically. A mutant that
fails to compile, does not differ from the baseline, or renders
identically to an earlier mutant is discarded, and a timed-out run counts
as killed. The operator set carries a version identifier, pinned by every
kill-sheet.

**REQ-harden-scope** (behavior): The `harden` operation MUST accept
requirement-set and changed-symbol scoping with a per-symbol mutant
budget, so the hot loop completes in seconds while the exhaustive sweep
remains available for the night tier.

**REQ-harden-staged-scope** (behavior): The `harden` operation MUST offer a
staged-delta classification, scoping the change set to the working tree
against `HEAD`, that reports for each changed implementation symbol — a
symbol whose body differs from its `HEAD` form, in a non-test source file,
since test sources are witnesses rather than mutation targets — whether
hardening covers it or the specific reason it does not: covered, a bound
`implements` symbol with a resolving witness whose class the body mutator can
break; no bound implementation; no resolving witness; a witness class outside
the body-mutation operator set; a generated or data-only file; or an
integration seam, a changed file that declares no such body. The
classification is exploration, never a gate (REQ-harden-exploration): its sole
purpose is to make the manual-mutation tail explicit, so the operator hardens
the covered surface and hand-mutates only what the report marks unreachable.

**REQ-harden-records** (behavior): A hardening record MUST be keyed by
the mutated symbol, pinning the symbol's body hash, the witness set it
ran against with each witness's body hash, the operator-set version that
generated its mutants, the mutant budget it ran under, and the identity
of the toolchain that executed the witnesses — the one input the tree
does not carry: the same body under the same witnesses kills differently
across toolchains — and carrying the mutant count, the kill count, and
each survivor. A record is stale when any pin no longer covers the
request — a new witness bound to the symbol, a change to a bound
witness's body, an engine gaining operators, a toolchain change, or a
request for more mutants than a capped sheet generated invalidates it
exactly as a body edit does. Pinning witness content, not merely the
witness set, is what re-stales a sheet when a bound test is
strengthened: an assertion added to a witness that would now kill a
recorded survivor moves that witness's body hash, so the sheet
re-measures rather than serving the survivor the current tests
demonstrably kill. Per-requirement views are derived from the binding
store on demand, never stored. Sheets are per-platform by construction —
the toolchain pin carries GOOS/GOARCH — so a team spanning platforms
regenerates from one designated platform (typically CI) rather than
ping-ponging the store. Because a kill-sheet is exploration output and
never gate input, an unrecognized field in one is discarded rather than
bricking the load: a sheet written before a pin existed re-stales — the
missing pin no longer matches — and re-measures, while any other dropped
field merely under-populates a record still served from cache. Both are
acceptable precisely because no verdict rests on a kill-sheet; the
authoritative record stores stay strict, where an unknown field would
silently drop a claim.

**REQ-harden-attestation** (behavior): An attested equivalence MUST be
recorded on the kill-sheet as a survivor disposition naming the mutant
and the reasoning, refused unless the mutant is among the sheet's
survivors, and shed whenever the sheet's pins move — every body, witness,
or operator version's equivalences are judged afresh, and a sheet's open
findings are its survivors less its attested ones. Positions are
location metadata, rebased against the sheet's recorded body anchor when
carried across regenerations: drift from edits outside the body never
sheds a disposition.

**REQ-harden-exploration** (behavior): Hardening campaigns MUST NOT feed
the gate — survivors are findings awaiting disposition, a strengthened
test or an attested equivalence, never automatic failures.
