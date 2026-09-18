# Scoped check fails on gap-excused staleness outside its scope

Lands: awaiting triage

## Failure and contract

A scoped check can fail even when its selected requirements pass and the
only unexcused outside red is a witness deliberately skipped by that scope.
The outside binding has stale consent, but a current gap explicitly excuses
`stale` and `uncovered`, not `broken`. Coverage classifies the scope-skipped
witness as broken, then refuses to mark the row scope-blocked because its
binding is stale. The later gap evaluation cannot excuse the broken bucket.
The scoped gate consequently counts a violation that would be excluded if
the already-excused staleness were accounted for in scope classification.

`REQ-check-verdict` in `docs/specs/change.md:422-436`, especially `:431-433`,
requires reds solely on the scope boundary to be classed scope-blocked and
excluded from the scoped verdict's undeclared-red term. This composes with
`REQ-gate-no-undeclared` at `:346-353` and `REQ-gap-consent` at `:198-214`:
the outside stale class is declared by a current gap, not an additional
undeclared red. No gap in this report authorizes broken implementation or
observed test failures. Do not broaden the gap to `broken` as a workaround.

## Provenance and field report

The reproduced executable is `/home/nikolas/.local/bin/stipulator`:

- SHA-256: `89a870dd7ca04b298ed9694fa5ffcf532349ff1d267a5b83b2494927f418e156`.
- `go version -m`: module `v0.64.6`, revision
  `e2f4fa881b4d68291df9120bde4d6a842b01d114`, `vcs.modified=false`.
- Build: `go1.27.0-X:nodwarf5`, `GOEXPERIMENT=nodwarf5`, Linux/amd64,
  `CGO_ENABLED=1`; dependency `github.com/greatliontech/gofresh v0.101.0`.
- The owning worktree was clean before `git pull --ff-only`, which advanced
  `597f633d97dbc983be6bd46702c7c13fe7fb931d` to
  `c9fcf5c80bd4800b4410a9e80cdb61214109516f` (`v0.64.7`). HEAD,
  `origin/main`, and FETCH_HEAD agreed at the latter revision.
- `git diff e2f4fa881b4d68291df9120bde4d6a842b01d114 HEAD --
  internal/coverage/coverage.go internal/check/check.go docs/specs/change.md`
  was empty. Source citations below refer to the pulled revision; those
  causal files are unchanged from the installed binary's source. This is
  not a claim that the installed binary is `v0.64.7` or that its other
  changes were executed by this reproduction.

The supplied Stash output is retained at
`/home/nikolas/.local/share/opencode/tool-output/tool_0b561a1cb001b2Hu8nBrwP5R1d`.
It reports `scopePartial: true`, `passed: false`, 39 executed subjects,
39 uncacheable, zero served witnesses, and zero outside-policy subjects.
The selected IDs are `REQ-builtins-context`, `REQ-builtins-dispatch`, and
`REQ-builtins-registration`; none appears among the 47 outside coverage
violations. The reported selected race witnesses passed, with selected
requirements' remaining reds gap-excused. Twelve outside rows are
scope-blocked and excluded; the remaining 35 combine stale binding consent
with scope-skipped witnesses and remain unexcluded. The selected counts
and violation list are visible at output lines 2167-2230; for example,
`REQ-exec-redirections` at 1368-1406 shows the stale-pin and scope-skip
reasons, the gap-class mismatch, and `scopeBlocked: false`.

This is a verdict-composition defect, not a claim that uncacheable subjects
should be served or that a conservative refusal is invalid. Reading the
saved output did not execute or modify Stash. All new execution below is
confined to a two-test fixture outside the consumer repositories.

## Causal path

- `internal/verify/verify.go:677-681` transfers the witness run's
  `ScopeSkipped` fact to the binding result.
- `internal/coverage/coverage.go:445-447` sets `e.stale` for old binding
  consent. At `:506-522`, the scope-skipped `TestNotRun` sets `e.broken`
  and increments `e.scopeSkipped`, without setting `e.otherRed`.
- The bucket selection at `:589-592` chooses broken before stale.
  At `:632`, `ScopeBlocked` requires `!e.stale`, even when the stale
  class is excused by a current gap. This decision precedes gap judgment.
- At `:639-657`, gap consent and excuses are evaluated. At `:687-716`,
  the excuse check sees the chosen broken bucket, which this gap does not
  excuse, and appends the outside requirement to `Violations`.
- `internal/check/check.go:375-389` excludes only violations whose rows
  already carry `ScopeBlocked`; `:291-304` uses that result in `Passed`.
  No later step reconciles the excused stale cause with scope classification.

The narrow obligation is to determine whether any unexcused non-scope
cause remains after applying valid gap consent and declared excuse classes.
It is not permission to suppress all outside-scope errors, drop `!e.stale`
unconditionally, or relabel real broken causes as scope boundaries.

## Isolated reproduction

Use a fresh directory, such as `/tmp/opencode/stipulator-scoped-gap`.
Create the following six files; the subsequent authoring commands create
binding and gap records only within this fixture. There are no external
Go dependencies, purity assertions, or toolchain overrides. The explicit
plain-witness admission avoids race instrumentation for these two tiny
tests and remains visible as `raceEnabled: false` on their result rows.

`go.mod`:

```go
module example.com/scopedgap

go 1.27.0
```

`selected/selected_test.go`:

```go
package selected

import "testing"

func TestSelected(t *testing.T) {
	if 2+2 != 4 {
		t.Fatal("addition failed")
	}
}
```

`outside/outside_test.go`:

```go
package outside

import "testing"

func TestOutside(t *testing.T) {
	if 3+3 != 6 {
		t.Fatal("addition failed")
	}
}
```

`.stipulator/manifest.textproto`:

```textproto
include: "spec.md"
```

`.stipulator/policy.textproto`:

```textproto
invocations {
  name: "plain"
  timeout { seconds: 20 }
  go {
    packages: "./..."
    plain_witness: true
    args: "-test.timeout=10s"
  }
}
```

Initial `spec.md`:

```markdown
# Scoped gap fixture

**REQ-fixture-selected** (behavior): The selected operation SHOULD add two and two.

**REQ-fixture-outside** (behavior): The outside operation SHOULD add three and three.
```

Run from the fixture directory in the order given. Each CLI call has a
positive outer deadline; the policy also bounds its test process.

```sh
timeout 30s /home/nikolas/.local/bin/stipulator bind --req REQ-fixture-selected --symbol example.com/scopedgap/selected.TestSelected --role tests --file .stipulator/bindings/selected.textproto
timeout 30s /home/nikolas/.local/bin/stipulator bind --req REQ-fixture-outside --symbol example.com/scopedgap/outside.TestOutside --role tests --file .stipulator/bindings/outside.textproto
timeout 45s /home/nikolas/.local/bin/stipulator check --ids REQ-fixture-selected --json
```

This is the pure scope-skip control. Observed: `passed: true`,
`scopePartial: true`, selected covered, outside broken but
`scopeBlocked: true`; one selected test executes, none is served, and the
outside binding has `contentPinned: true`, matching shape, and
`TEST_OUTCOME_NOT_RUN`. The ordinary coverage `gatePasses` remains false;
the scoped verdict correctly excludes that outside violation.

Change only the outside requirement's sentence in `spec.md` to:

```markdown
**REQ-fixture-outside** (behavior): The outside operation SHOULD add three and three and preserve the result.
```

Leave both binding records untouched. Run the unexcused-stale control:

```sh
timeout 45s /home/nikolas/.local/bin/stipulator check --ids REQ-fixture-selected --json
```

Observed: `passed: false`, `scopePartial: true`; selected remains covered,
outside has `contentPinned: false`, matching shape,
`TEST_OUTCOME_NOT_RUN`, bucket broken and `scopeBlocked: false`. This
failure is correct: there is not yet a gap excusing its stale consent.

Declare the gap against the revised requirement, without re-consenting
the old binding, then repeat the scoped check:

```sh
timeout 30s /home/nikolas/.local/bin/stipulator gap --req REQ-fixture-outside --reason "The outside witness has not been reconciled with the revised requirement." --manual "The outside witness is reconciled with the revised requirement." --excuses stale --excuses uncovered
timeout 45s /home/nikolas/.local/bin/stipulator check --ids REQ-fixture-selected --json
```

The binding's original content hash is
`c74cafb862c1c7f547762d90c8cfa5b357587649745864a49acac52ebe75378e`;
the new gap's current content hash is
`657743a896e441bab25186ea8c13345f284b2e6d677406f827bfab790318a426`.
Their consent-source pins also differ. The gap contains exactly
`GAP_EXCUSE_UNCOVERED` and `GAP_EXCUSE_STALE`, with an unfired manual
condition. This establishes real old binding consent and current gap
consent, not unset-pin compatibility behavior or a fabricated hash.

Expected: `passed: true`, `scopePartial: true`, and the outside red classed
scope-blocked for the scoped undeclared-red term. Selected is covered;
the outside witness stays unexecuted. Actual relevant fields:

```text
passed: false
scopePartial: true
scopeIds: [REQ-fixture-selected]
coverage.gatePasses: false
coverage.violations: [REQ-fixture-outside]
coverage.gaps[outside]: state=GAP_STATE_OPEN, staleConsent=false
coverage.requirements[selected]: bucket=BUCKET_COVERED
coverage.requirements[outside]: bucket=BUCKET_BROKEN, scopeBlocked=false
verify.results[selected]: contentPinned=true, testOutcome=TEST_OUTCOME_PASSED
verify.results[outside]: contentPinned=false, shape=SHAPE_STATE_MATCH,
  resolution=RESOLUTION_RESOLVED, testOutcome=TEST_OUTCOME_NOT_RUN
testsExecuted: 0
testsServed: 1
testsOutsidePolicy: 0
testsUncacheable: 0
```

The outside reasons name the stale binding, the witness not executed
because it is outside the check's ID scope, and the gap excusing
`uncovered, stale, not broken`. No verification problem or observed
execution failure explains the verdict. The selected witness was already
granted by the first control and is served in this call; the outside
witness has never executed. The JSON does not expose a per-binding
`ScopeSkipped` field; the scope reason and the source path above establish
that internal classification.

Finally, run the default unscoped witness-evidence check, **not** `--full`:

```sh
timeout 45s /home/nikolas/.local/bin/stipulator check --json
```

Observed: `passed: true`, `coverage.gatePasses: true`, no coverage
violations, one test executed (outside), one served (selected), and both
test outcomes passed. The outside binding is still `contentPinned: false`,
but its bucket is now stale, excused by the same current gap. The gap
remains open; there is no prune residue. This demonstrates that the
non-scope stale cause is declared without repairing or re-pinning it.
Run this control last: once the outside witness has fresh evidence, a
subsequent scoped call can serve it and no longer exercises a scope skip.
Use a fresh fixture directory to repeat the complete sequence.

## Required regression coverage

- Add an isolated coverage/check regression with a passing selected
  requirement, old outside binding consent, current outside gap excusing
  stale/uncovered but not broken, and an outside scope-skipped witness.
  The verdict must pass as partial without executing that outside witness.
- Preserve the passing pure scope-skip control, the failing unexcused-stale
  control, and the passing default unscoped control. Existing
  `internal/check/serving_test.go:156-163,279-303` covers missing fresh
  witness evidence outside the scope, not this combination of stale
  binding consent and a current stale-class gap.
- Stale gap consent must continue to suspend its excuse. Merely having a
  gap record, or an uncovered-only gap, cannot excuse a stale binding.
- A failed witness actually observed by the scoped pass must still fail
  it. Static missing symbols, shape mismatches, dangling enforcement
  pointers, policy-selection faults, and other unexcused non-scope causes
  must not become scope-blocked just because a scope-skipped witness is
  present on the same requirement.
- Cover the effective violation classification in the shared core and
  the partial verdict on both CLI and MCP surfaces, retaining the ordinary
  coverage report versus scoped-verdict distinction. No need to execute
  every negative case through the CLI if focused unit tests pin it.

Only the three controls and the failing case above were executed for this
filing; the additional negative cases are obligations for the owning
repair. No production repair, consumer record change, whole-consumer test
run, `--full` invocation, race campaign, or mutation campaign is included.
