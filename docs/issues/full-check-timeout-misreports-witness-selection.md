# Full check misreports selected but unreached witnesses as outside policy

Lands: cross-tool train chunk 247 (Stipulator outside-policy accounting, A1).

## Fault

A full, health-judged check can report a bound witness as outside the
policy's witness-eligible selection when its package was selected by an
eligible race invocation but the package timed out before reaching it.
The coverage reason tells the caller to cover the package with a race
invocation or explicit plain-tier admission, although it already has race
coverage. Selection eligibility and whether execution reached a subject
are distinct facts; absence of an execution row does not establish the
former.

This contradicts the selection boundary in `docs/specs/change.md`
(`REQ-check-witness-selection`): subjects selected only by ineligible
invocations, or by none, are outside selection. An eligible subject not
reached before a timeout instead needs the producing execution's cause.
The full-check health and retained-output contracts remain in force
(`REQ-check-verdict`, `REQ-check-diagnostics`, and
`docs/specs/overview.md`'s `REQ-core-one-execution`).

## Observed evidence

The consumer report used installed Stipulator revision
`20fb82c3c609`, reported binary SHA-256 prefix `ef91`, GoFresh `0.101`,
and Go `1.27` with `nodwarf5`. The binary prefix is abbreviated provenance,
not a complete digest. Source references below are at owning-repository
revision `20fb82c`.

Stash's accepted policy had one invocation, `race`, selecting `./...`
with `race: true`, a `test_binary_timeout` of `2m`, and an invocation
envelope of 300 seconds. Under the reported loaded full-race run:

- The check returned `passed: false` and `suiteHealthJudged: true`.
- All 21 packages were reported; `internal/sh/expand`,
  `internal/sh/interp`, and `internal/sh/syntax` had
  `HEALTH_DISPOSITION_TIMEOUT`, at 120.052s, 120.070s, and 120.360s.
- Each timeout diagnostic retained its package output and running-test
  roster, with `truncated: false`. The report completed in 2m47.3s.
- The `interp` execution report retained 1,180 test rows. Five bound
  subjects had no execution rows because execution had not reached them:
  `FuzzAliasExpansionPreservesProgram`, `FuzzLiteralBytes`,
  `TestAliasExpansionPreservesProgram`, `TestLiteralBytes`, and
  `TestQuotedHeredocBytes`.
- All five received the reason below despite their package's eligible
  `race` invocation. The result reported `testsExecuted: 840` and
  `testsOutsidePolicy: 284`; those aggregate subject counts are not the
  retained row count, and this report does not attribute all 284 to the
  five demonstrated misclassifications.

```text
bound test <symbol> is outside the policy's witness-eligible selection - witness evidence derives only from race: true invocations or explicit plain_witness: true admissions; cover its package with one
```

The original consumer evidence is machine-local at
`/home/nikolas/.local/share/opencode/tool-output/tool_098278890001cHCQkgvzfpRlEw`.
The relevant facts are copied above so this issue does not require that
external artifact to remain available. The report demonstrates a wrong
classification and repair instruction, not an unsound green result,
lost execution results, or a cause of content/shape hash changes.

## Current causal path

In `internal/backends/golang/derive.go`, `ExecutePolicyWitnessed`
executes the accepted policy and derives its test run before classifying
outside subjects. Lines 1319-1332 populate `eligibleCovered` only from
`report.GetTests()` rows whose producing invocation is race or admitted
plain. Lines 1339-1347 then walk the discovered obligation universe and
mark every test/fuzz subject absent from that map as outside selection.
Thus a selected but unreached subject takes the same branch as a subject
with no eligible invocation. Lines 1350-1351 publish that set and count.

In `internal/coverage/coverage.go`, the `verify.TestNotRun` branch at
lines 506-510 reads `OutsideWitnessSelection` and emits the incorrect
repair instruction quoted above. The narrow repair boundary is the
distinction between policy eligibility and observed execution completion,
including propagation of the producing invocation/package failure cause;
it is not a consumer policy admission change.

## Required upstream regression

Use an owning-tool fixture, not another Stash measurement, to establish
the repair before consumer adoption:

1. Define an eligible race invocation selecting a fixture package, with
   a finite test-binary deadline shorter than its invocation envelope.
   Arrange an early blocking test, or deterministic deadline exhaustion,
   before a later discovered and bound witness can run.
2. Run the full-check path. Assert that the later witness is selected but
   unexecuted, its diagnostic identifies the producing invocation and
   package timeout, and it does not advise adding already-present race
   or plain-witness coverage. Its absence must not inflate the
   outside-selection count or class its requirement as policy-blocked.
3. Assert that full-check suite health and the verdict remain failing,
   that no passing outcome is invented for the unreached witness, and
   that every timed-out package's disposition and retained diagnostic
   output still reach the result. Include multiple timed-out packages
   when exercising the report projection.
4. Keep a genuinely ineligible/unselected subject as a boundary control:
   it must retain the outside-selection classification and appropriate
   admission advice. Preserve freshness/cache refusals, producer
   validation, conservative dynamic-state/callback/external-input and
   unsupported-selection refusals, and the one-execution health rule.

Aborted-after-start and fail-before-run fixtures can additionally pin
the same distinction, but the demonstrated minimum is an eligible
subject never reached before package timeout. Cancellation remains a
separate operational abort, not a completed failing verdict. No change
to consumer policy, evidence pins, or purity assertions establishes this
repair.
