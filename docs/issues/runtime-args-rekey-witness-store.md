# Runtime-only args re-key the witness store — a budget edit discards every record

Reviewer-verified (toolchain-guard change set, 2026-08-26): the
capture-group key folds the invocation's whole `Args` list
(derive.go's groupIdentity, `args=` component), and record identity is
group-scoped (the version-7 coordinate). Editing
`-test.timeout=30m` → `100m` in the accepted policy therefore made
every stored record unaddressable, forcing a full cold re-execution
(446 witnesses, ~31m wall) for a knob that changes neither the build
nor which tests run. The justifying comment ("two invocations
differing only there build or run two different things") holds for
`-tags`/`-gcflags`/`-pgo` and is false for runtime bounds.

Failure direction is cost, not correctness — fail-closed
re-execution — but it means no execution budget can be tuned without
a full cold run: structural pressure to over-size budget knobs.

Fix shape: partition `Args` into build-affecting and runtime-only
(the classification must fail closed — an unrecognized argument stays
identity-bearing), keying the group identity on the build-affecting
partition alone. Records survive budget edits; the partition table is
the reviewed surface.

Lands: the next chunk that touches the capture-group identity or the
record coordinate.
