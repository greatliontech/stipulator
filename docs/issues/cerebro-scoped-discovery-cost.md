# Cerebro: one-requirement check exhausts ten minutes in discovery

An IDs-scoped check on Cerebro with the current inspected Stipulator build exhausted
a 600-second execution budget before running any witnesses, while specification
compilation and records-only verification completed. Attribute the discovery cost
and establish a bounded affected-requirement workflow without weakening evidence.

Lands: the next Go-backend discovery/performance slice reproducing Cerebro's
`REQ-arch-obligation-activity-perimeter` scoped check with the binary, selection,
phase and resource evidence below; the tooling driver owns attribution and the
bounded-workflow remedy before claiming scoped execution is practical for this corpus.

## Reproduction identity

Field report from `github.com/candosa/cerebro`, 2026-10-12. The observed candidate
was based on commit `eff4878c04739e3e70e27c28b5664812e733c801`, with the reviewed
but uncommitted cumulative modelo 130 repair: six Go source/test files initially,
then a shared dwelling comment, IRPF corpus prose and one test binding. The repair
is not part of that baseline commit. The corpus had 427 bound requirement IDs and
3,010 binding rows; its full accepted test policy covers three workspace modules.
The exact external evidence and patch remain on the reporting machine (below).
Checking the baseline commit is a useful control, not an exact candidate replay.

The **explicit binary used for the scoped observation** was:

- `/home/nikolas/.local/bin/stipulator`;
- module version `v0.73.12-0.20261010191426-c9ea6cd24f0e`;
- revision `c9ea6cd24f0ed51e3e5d2db3efe71b60c214d531`;
- binary SHA-256 `38659b6cd330fc66fc83b8bf9162188097a418bfc16ac4724c1a649dd317b3b5`;
- built with `go1.27.1-X:nodwarf5`, gofresh `v0.115.1`, Linux/amd64.

The recorded environment was `GOMAXPROCS=4`; `GOFLAGS`, `GOTOOLCHAIN`,
`GOEXPERIMENT` and `TMPDIR` were unset. The accepted policy had `plain_witness:
true`, root/primitives/simulator package selections and `-test.timeout=30m`.
No race, observation-bracket, vouch or policy changes were made for the run.

**Version trap:** bare `stipulator` on the same host resolves first to
`/home/nikolas/go/bin/stipulator`, version `v0.73.6`, revision
`c01a2987170cb5610521e7b737e25cc2ffa2c5ce`, using gofresh `v0.112.1`.
Older full-run timings below belong to that binary, not c9ea6cd. Retain
`type -a stipulator` and `go version -m "$STIPULATOR"` before comparing runs;
do not infer executable identity from the adjacent checkout or MCP process.

## Observed commands and outcomes

Each command ran serially under the host's shared `mlock run` discipline. The
execution timeout starts after acquiring the lock. These are observations, not
quiet benchmark measurements: unrelated host/cache effects were not controlled.

| Explicit c9ea6cd command | Elapsed | Outcome |
|---|---:|---|
| `compile` | 4.522 s | Exit 0 |
| `verify --no-test --view bindings --json` on candidate | 159.192 s | Exit 0; hygiene data, not coverage acceptance |
| Same records-only command on isolated baseline | 101.562 s | Exit 0 |
| `check --ids REQ-arch-obligation-activity-perimeter --json` | 600.898 s | Supervisor exit 124; cancelled in discovery, no witness execution |
| Records-only verification after selected binding reconciliation | 32.179 s | Exit 0; different bindings/cache state, not a controlled speed comparison |

The scoped check's complete phase account is short:

```text
phase compile (0s) — at the start: resident 32 MiB (peak 32 MiB), ceiling 4.4 GiB
phase discovery (2m38.3s) — at compile's exit: resident 661 MiB (peak 3.7 GiB), ceiling 4.4 GiB
gofresh: cancelled — 0 observability proof slices and 0 scans persisted this operation; a rerun serves them
cancelled in the discovery phase; kept nothing — at the end: resident 5.0 GiB (peak 5.3 GiB), ceiling 4.4 GiB
```

Thus roughly 7m22s of the bounded run remained in discovery after the preceding
phase. The first phase is not pure Markdown compilation: standalone `compile`
completed in 4.522s. The reported peak exceeding the soft ceiling does not by
itself establish a failed hard limit, OOM, leak or excessive single allocation.

Runnable form from a materialized candidate checkout:

```sh
STIPULATOR=/absolute/path/to/the/identified/stipulator
go version -m "$STIPULATOR"
mlock run timeout --kill-after=20s 600s env GOMAXPROCS=4 \
  "$STIPULATOR" check --ids REQ-arch-obligation-activity-perimeter --json \
  > scoped.json 2> scoped.log
```

Capture the actual exit status separately. Do not repeatedly retry unchanged
discovery, weaken the test selection, or interpret an incomplete JSON/output as
a passing scoped verdict. The observed run used an external Python supervisor
with a process group, TERM and a 20-second KILL fallback; the command above is
the equivalent bounded CLI experiment, not a claim of byte-identical supervision.

## Related full-run evidence, older binary only

A completed candidate full run on v0.73.6 took 19m57.5s: compile 31s, discovery
26.4s, execution 19m. It reported 140/140 packages, 8,321 executed witnesses and
8,304 uncacheable witnesses, then failed on coverage/binding residue. The isolated
baseline full attempt reached discovery at 15.9s and execution at 17m41.6s, then
was cancelled at 22m6s. Its final 140/140 cancellation counter was not completion.
These differences have not been attributed to cache, host load or a code change.

The completed old run's execution-exit resident was 475 MiB; final resident was
355 MiB, with peak 1.1 GiB attributed to execution's exit. The cancelled baseline
reported final resident 404 MiB and peak 7.4 GiB at discovery's exit. This supplies
phase-attributed evidence for the existing
[resolver resident-set issue](resolver-child-resident-set.md); it does not
reproduce that issue's earlier 6.9 GiB execution-parent growth.

## Attribution required before a mechanism change

At c9ea6cd, `internal/check/check.go` and `internal/backends/golang/derive.go`
show that preparation includes typed resolution, witness classification/seeding,
views, fingerprints and freshness analysis. `--ids` restricts stale witness
execution; it does not promise that all discovery is confined to those IDs.
`verify --req` is a report filter, not a cheap execution-scope substitute.

Collect subphase timings or profiles that distinguish package loading/resolution,
per-witness seeding walks, dynamic-state analysis, scan/discharge and proof
preparation on identified cold and warm states. Retain parent/resolver/child
resource measurements separately. Establish which work can legitimately be
shared, reused or limited to affected subjects before altering scope. A new
progress counter alone does not establish bounded completion, but an attributed
remaining-work account is useful when a budget interrupts the pass.

Acceptance requires a representative scoped check to reach and complete its
selected witnesses within a recorded practical budget on this corpus, preserving
all serving/refusal soundness and honest cancellation. Do not make a timeout
green, hide missing evidence or use a permissive vouch to avoid analysis. A
complete full healthy check remains a separate restoration test.

## Evidence handoff

On the reporting host, `/home/nikolas/.cache/cerebro-tmp/cerebro-deep-audit/`
contains `newer-scoped-perimeter-{execution.json,log}`,
`newer-{repair,baseline}-bindings{.json,-execution.json,.log}`,
`newer-compile-text-execution.json`, `baseline-full.log`,
`130-stipulator-full-completion.log`, `binding-comparison.md`,
`reconciled-binding-comparison.md`, `130-before-generation.diff` and the
reproduction overlays/reports. These paths are optional local evidence locators;
the decisive observations are embedded above so the issue stands alone.
No customer data is required for the reproduction.

Cerebro's stale consent/shape records and missing enforcement are separate
repository debt, not diagnosed tool bugs. Its temporary delivery rule requires
direct tests/builds/vet/generation/architecture, genuine affected-clause witness
review and mutation evidence; it does not certify a full Stipulator pass.
