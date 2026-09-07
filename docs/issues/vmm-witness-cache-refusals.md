# VMM passes the policy while every executed witness remains uncacheable

Lands: user decision

## Consumer Evidence

VMM (`github.com/thegrumpylion/vmm`, commit `7ac2e7a`) passes its accepted
ordinary and race policy, selecting both `./...` and `./testdata/linux-init`.
With the installed Stipulator revision `2e332e2c0841`, gofresh v0.98.0, and
`go1.27.0-X:nodwarf5` / `GOEXPERIMENT=nodwarf5`, a check reported zero fresh
witnesses served and 536 executed witnesses, all uncacheable. These are
previously observed results, not new executions made while filing this report.
The repository was pulled to `1d01266` before filing; the pull changed issue
documentation only and the dependency remains gofresh v0.98.0.

The notice says the release is not listed and disables standard-library
observation admission. The existing shared root-cause report is:

https://github.com/greatliontech/gofresh/blob/main/docs/issues/nodwarf5-toolchain-audit-key-mismatch.md

This issue owns Stipulator's adoption and consumer integration, not a second
toolchain parser. Both release/selection lookup and baked-experiment handling
must agree in the adopted dependency.

The reported post-run refusal groups were:

| Preferred reason | Witnesses |
|---|---:|
| `reaches go:linkname (opaque linkage)` | 355 |
| `testing runtime value escapes analyzable receiver` | 125 |
| `reaches os.ReadFile (file I/O)` | 17 |
| Shared dynamic state: `golang.org/x/tools/internal/gcimporter.exportMap` | 14 |
| `reaches testing.Fuzz (test runtime execution)` | 12 |
| `reaches unaudited standard operation context.Background` | 6 |
| `reaches syscall (external system call)` | 5 |
| `reaches unaudited standard operation bytes.Equal` | 2 |

These counts classify the preferred reported reasons. They are not independent
root causes or a prediction of which refusals survive an audit-key repair.
Historical invocation shapes were `env -u VMM_UPDATE_GOLDEN stipulator check`
and `env -u VMM_UPDATE_GOLDEN stipulator check --full --quiet`. The consumer's
`.stipulator/policy.textproto` and its recorded bindings identify the selection.
VMM measurements are paused pending upstream resolution; reduce cases into
tool-owned fixtures instead of using a new VMM witness run as the filing step.

## Separate Admission Questions

- A corrected key can restore only the dependency's specifically audited
  standard operations and linkage forms. In v0.98.0, `bytes` is in the
  source-only standard-package set while `context` is not; these two reasons
  cannot be treated as the same repair.
- Testing-handle escape in the maximal scan is distinct from the subject-level
  observed proof. Trace the actual selected runnable, grant, attached
  observation and publication refusal instead of assuming either a bad test
  or a faulty refusal from that wording alone.
- Full-policy packages with multiple selected top-level runnables do not
  automatically gain the observed proof that requires one selected runnable.
  Compare the selective and full paths under their actual producing-process
  conditions; do not manufacture singleton provenance for a grouped run.
- File reads, syscall effects, fuzz execution, and `gcimporter.exportMap`
  mutation have separate proof boundaries. Determine whether each remaining
  case is legitimate conservatism, a consumer integration requirement, or a
  demonstrated upstream precision defect. No blanket vouch is justified by
  this report, including for the named dynamic map.

## Required Resolution

Adopt the fixed gofresh version, rebuild the installed CLI, and verify the
actual release/experiment selection. Add reduced regression coverage for the
audited-selection notice and demonstrated consumer publication failures.
Exercise selective and full checks without conflating execution success,
witness classification, process health and reusable freshness. Preserve
per-subject actionable refusal attribution where reuse is legitimately
unavailable; making every witness cacheable is not the acceptance criterion.

Relevant ownership is in `internal/backends/golang/derive.go`, `publish.go`,
`normalize.go` and `explain.go`, governed by the freshness and observed-proof
clauses in `docs/specs/evidence.md` and the policy-notice clause in
`docs/specs/change.md`. The running-toolchain branch in
`selectionnotices_test.go` can skip on an unlisted toolchain, so a general green
suite alone is not proof of audit admission.

[Publication-refusal consolidation](publish-refusal-ladders.md) is related but
does not resolve this consumer report. The earlier corpus-pin-lag issue covered
an older dependency adoption and must not be revived as a substitute. No
requirement gap, weakened policy, downgraded Go selection or generic vouch
should be added merely to suppress these diagnostics.

## Additional Stash Evidence And Adoption Acceptance

Stash independently reproduced the same notice with the installed CLI build
`v0.61.2-0.20260907145236-2e332e2c0841`, linked to GoFresh v0.98.0, and the
ambient `go1.27.0-X:nodwarf5` toolchain. Its recorded full check at local revision
`5b53500` passed with 434 executed race witnesses. Both the current CLI and MCP
reported the audit-key warning; older MCP binaries were also present, but the
current CLI reproduction rules out a server restart alone as the repair.

The dependency's release and selection tables spell the flavor
`go1.27.0 X:nodwarf5`, while the reported identity uses a hyphen before `X:`.
`binaryExperiment` also recognizes only the space form. Stock Go 1.27.0 is
already listed. The existing GoFresh report owns this three-path source repair;
Stipulator owns adopting the released dependency and proving its integration.

The shared consumer-adoption acceptance also needs:

- an identifiable Stipulator release containing the repaired GoFresh version;
- real ambient default/race selection coverage, including matching inherited and
  explicit experiment settings, rather than relying only on injected notices;
- unlisted-release, mismatched-experiment, and unwalked-tag negative controls,
  with CLI/MCP notice attribution preserved;
- an eligible deterministic witness that publishes and reuses observation proof
  without purity or dynamic-state assertions; warning disappearance or a green
  full-policy check alone is insufficient;
- installed CLI and MCP process provenance matching the repaired builds.

Stash's module remains at Go 1.27.0 with no separate policy toolchain pin. Its
measurements are blocked until the upstream resolutions are released/adopted and
installed; use tool-owned regression fixtures, not a blocked-application canary.
This shares the adoption work already owned by this report, without predicting
that all 434 Stash witnesses or all VMM refusal groups become cacheable.
