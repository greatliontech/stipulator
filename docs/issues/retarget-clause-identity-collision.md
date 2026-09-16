# Retarget mistakes distinct clause claims for a global collision

Lands: awaiting triage

## Impact and provenance

An unrelated, valid pair of clause-scoped claims prevents a symbol rename
anywhere in the binding store. The Stash field report names two
`implements` claims for `REQ-builtins-context`, both on Go symbol
`github.com/thegrumpylion/stash/internal/sh/interp.commandState.claim`, at
`.stipulator/bindings/builtins.textproto:429` and `:473`, scoped respectively
to `prepared-operations` and `grant-lifetime`. Previews of both
`runtime.ExternalExecutor.Execute` to `shell.ExternalExecutor.Execute`
and `runtime.BuiltinContext` to `builtins.BuiltinContext` refuse on that
unrelated pair, blocking 19 intended move claims in the reported work.
These are field-report facts; the reproduction below uses no Stash tree,
records, or measurements. The reported previews performed no writes.

Audited installed CLI:

- Path: `/home/nikolas/.local/bin/stipulator`.
- SHA-256: `89a870dd7ca04b298ed9694fa5ffcf532349ff1d267a5b83b2494927f418e156`.
- `go version -m` module version: `v0.64.6`.
- VCS revision: `e2f4fa881b4d68291df9120bde4d6a842b01d114`;
  `vcs.modified=false`; `vcs.time=2026-09-14T15:49:42Z`.
- Build toolchain: `go1.27.0-X:nodwarf5`; `GOEXPERIMENT=nodwarf5`;
  `GOOS=linux`, `GOARCH=amd64`, `CGO_ENABLED=1`.
- Owning source after `git pull --ff-only`: HEAD and `origin/main` both
  equal that revision, tagged `v0.64.6`; worktree was clean before filing.
- The CLI has no `version` subcommand; provenance comes from the executable
  hash and Go build metadata, not an inferred version string.

## Contract and cause

Source references below are at the audited revision.

`docs/specs/evidence.md:48-64` (`REQ-evidence-clause-claim`) makes the
**resolved clause** part of claim identity: distinct clauses are distinct
claims, while a label and ordinal naming the same clause are one claim.
`internal/verify/verify.go:512-521` honors that rule through
`records.ClaimClauseKey`; its implementation at
`internal/records/clause.go:74-109` resolves labels to ordinals and keeps
unscoped claims distinct.

The CLI enters `author.Retarget` from `internal/cmd/retarget.go:40-47`.
`internal/author/author.go:511-515` first calls `retargetBindings`.
Its local identity at `:742-745` has only requirement, backend, symbol,
and role. At `:754-766`, every stored binding, including bindings outside
the selected prefix, enters the same post-rewrite `seen` map; `:762`
omits clause identity and `:763-764` refuses the second distinct-clause
claim. This happens before replacement resolution at `:772-787` and
before application. The guard therefore rejects a store the existing
verification hygiene accepts, even when the requested rename does not
touch either claim.

There is also a contract inconsistency to resolve in the owning repair:
`docs/specs/change.md:99-114` (`REQ-change-retarget`) lists the same four
collision coordinates without the resolved clause, unlike
`REQ-evidence-clause-claim`. Align the retarget clause with the established
claim identity; do not remove support for distinct clause claims to make
the implementation fit its current key.

A raw clause spelling is insufficient: label/ordinal aliases must still
collide. The current prefix-only repair path deliberately avoids corpus
compilation when no member name changes (`author.go:517-529`). The repair
must address the corpus information needed for resolved clause identity
without silently changing unrelated repair-path behavior or bypassing
existing duplicate validation.

## Isolated reproducer

Create a fresh directory outside all consumer repositories, for example
`/tmp/opencode/stipulator-retarget-clause`, with these five files. No
third-party dependencies, witnesses, pins, or policy are needed.

`go.mod`:

```go
module example.com/fixture

go 1.27.0
```

`fixture.go`:

```go
package fixture

func Claim() {}
func Old() {}
func New() {}
```

`.stipulator/manifest.textproto`:

```textproto
include: "spec.md"
```

`spec.md`:

```markdown
# Fixture

**REQ-fixture-context** (behavior): The implementation MUST support both operations:

- **prepared-operations**: Prepare operations.
- **grant-lifetime**: Bound the grant lifetime.

**REQ-fixture-move** (behavior): The implementation MUST execute an operation.
```

`.stipulator/bindings/fixture.textproto`:

```textproto
bindings { requirement_id: "REQ-fixture-context" backend: "go" symbol: "example.com/fixture.Claim" role: BINDING_ROLE_IMPLEMENTS clause_label: "prepared-operations" }
bindings { requirement_id: "REQ-fixture-context" backend: "go" symbol: "example.com/fixture.Claim" role: BINDING_ROLE_IMPLEMENTS clause_label: "grant-lifetime" }
bindings { requirement_id: "REQ-fixture-move" backend: "go" symbol: "example.com/fixture.Old" role: BINDING_ROLE_IMPLEMENTS }
```

Run from that directory, with a positive wall-clock bound on every call:

```sh
timeout 30s /home/nikolas/.local/bin/stipulator -C "$PWD" compile
timeout 30s /home/nikolas/.local/bin/stipulator -C "$PWD" verify --no-test --json
timeout 30s /home/nikolas/.local/bin/stipulator -C "$PWD" retarget --from example.com/fixture.Old --to example.com/fixture.New --check
```

Observed compile result: `ok: 1 documents, 2 requirements, 0 terms, 0
notes, 0 annotations, 0 edges`. Records-only verification reports
`problems: 0`, `broken: 0`, `stale: 3`, and `shapeUnpinned: 3`: the
deliberately unpinned fixture is not coverage evidence, but its distinct
clause claims are not duplicate or malformed bindings.

Expected preview: one `REQ-fixture-move` rewrite from `Old` to `New`,
`check only: 1 binding(s) would retarget`, with all input files unchanged.
Actual:

```text
stipulator: retarget collides: the post-rewrite store would carry REQ-fixture-context go example.com/fixture.Claim BINDING_ROLE_IMPLEMENTS twice
```

The following controls were also run with the same bounded preview command,
each starting from the above binding input:

| Input change | Observed result |
| --- | --- |
| Delete only the second binding | Expected one-binding preview succeeds; destination resolution is not the cause of failure. |
| Replace only the second binding's clause with `clause_ordinal: 1` | Preview refuses the existing duplicate on `Claim`; `verify --no-test --json` also refuses with `duplicate binding`, naming clause 1 `prepared-operations`. |
| Set the first binding's symbol to `example.com/fixture.Old`; set the second to `example.com/fixture.New` and its clause to `clause_ordinal: 1` | Preview refuses the genuine post-rewrite collision on `New`. |

All retarget invocations used `--check`; no retarget apply was run. No
measurement campaign or Stash canary was used.

## Required regression coverage

- Accept the isolated valid store when an unrelated symbol moves. Also
  accept rewriting a shared symbol whose claims name distinct resolved
  clauses, including claims split across binding files.
- Refuse a rewrite that collapses two claims onto the same resolved clause,
  both with identical spelling and with label/ordinal aliases.
- Keep refusal of genuine pre-existing duplicates outside the rewrite
  selection consistent with verification hygiene; do not fix the false
  positive by skipping all unaffected claims in the global check.
- Preserve unscoped identity, backend/role distinctions, boundary matching,
  destination validation, pin semantics, and all-or-nothing application.
  Keep check previews write-free on both success and refusal.
- Cover the shared authoring operation and both CLI/MCP preview surfaces.
  Existing collision coverage in `internal/author/retarget_test.go:76-84`
  uses unscoped claims only and does not pin the distinct-clause case.

This issue requests an owning-tool repair and regression tests, not a
consumer record workaround, evidence re-pin, or relaxed duplicate policy.
