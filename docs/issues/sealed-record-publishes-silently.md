# Out-of-bracket observed reads serve with a silent read-to-seal-window residual

## The divergence

docs/specs/evidence.md (REQ-evidence-witness-freshness) states: "a read
resolving outside the declared root seals per-identity unverifiable —
permanently uncacheable under this root policy — both toward
re-execution, never reuse."

The implementation does something different. A witness reading an
out-of-bracket path that no exemption class admits publishes on the
plain-form path (`publishExecuted`, internal/backends/golang/
witnessrun.go): the path enters the record's manifest with a content
digest, the "not covered by observation bracket" note rides along as
manifest content that does not block validity, and the record SERVES on
later runs while the digest matches — staling correctly when the
content changes afterwards ("moved: path …" attribution works). The
observation-validity gate (`validatedObservation`) runs only on the
proof-attached leg, which the plain form never enters.

Demonstrated: a pure-marked test reading a file outside its package
bracket → first run publishes (Uncached=0, no reasons), second run
serves (Fresh=1), third run after changing the file re-executes with
the moved path named. Same behavior on gofresh v0.16 and v0.19; not
introduced by the root-class wiring.

## Why it matters

The bracket exists to bind the read-to-seal window: for in-root reads,
a change persisting across the run-to-ingest span moves the bracket and
the observation seals. Out-of-root reads have no bracket, and the
digest is computed at ingest — after the run — so a file that changed
BETWEEN the test's read and the seal binds the post-change content to
the pre-change outcome: spurious reuse until the file moves again. The
spec accepts window residuals only where it says so (the `.`/`.git`
exclusion, the exemption classes' stated assumptions); this one is
accepted silently, and the spec text says these reads never serve at
all.

## Resolution routes (spec-amend channel — user decides)

- Enforce the spec as written: refuse serving (or publication) on
  bracket-uncovered identities. Cost: every out-of-bracket-reading
  witness re-executes forever — measured over the gofresh corpus before
  the root-class work, 464 of ~478 witnesses were uncacheable and
  out-of-bracket runtime reads (build cache, temp, machine facts)
  dominated the refusals.
- Amend the spec to the implemented shape: out-of-bracket reads are
  observed, digest-bound, and serve, with the read-to-seal-window
  residual named as an accepted assumption-scoped spurious-reuse
  direction alongside the existing ones.
- Middle: serve only identities whose digests ALSO revalidate at a
  pre-spawn capture (extending the bracket's span to out-of-root
  identities), degrading to re-execution when the two captures
  disagree.

Reproducer state: `TestGoRunWitnessesExemptionBoundariesStayObserved`
(internal/backends/golang/witnessrun_test.go) pins the halves both
routes agree on (observed by identity, change-stales); the serving
behavior itself is deliberately unpinned pending this disposition.

Lands: when the user dispositions the spec-amend candidate, or at the
stipulator issue-elimination phase, whichever comes first.
