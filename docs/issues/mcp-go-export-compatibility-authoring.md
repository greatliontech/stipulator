# Installed MCP Go export compatibility blocks binding authoring

Lands: tooling-redesign 5.5a

Closure condition: the same identified live MCP server admits the reported
Go export format and successfully authors the blocked claim under recorded
server/build, selected project and toolchain provenance. The slot owns this
authoring-boundary investigation; it does not authorize actions in Weaver.

## Observed failure and missing claim

In the Weaver checkout at
`/home/nikolas/repos/github.com/thegrumpylion/weaver`, the first claim attempted
through `functions.stipulator_bind` was for requirement `REQ-causal-membership`
and test symbol
`github.com/thegrumpylion/weaver/internal/invocation/gmdbstore.TestCausalMembershipTraceProperty`.
The reported failure was:

```text
anchored.go:4:2: could not import bytes (reading /home/nikolas/.cache/go-build/49/4975787ef483fa43409fd019eafe50cf72ae1ee1594e9843fc7e8df181e844f3-d: internal error in importing "bytes" (cannot decode "bytes", export data version 5 is greater than maximum supported version 4); please report an issue)
```

No bindings were written. The requirement is declared in Weaver's
`docs/specs/causal-membership.md`; its intended test claim remains unauthored.
The failure above is the supplied observation, not a reproduction performed for
this filing. It establishes an export-reader compatibility failure on the
authoring path, not a failing test or an invalid requirement.

The user approved records-verification deferral, tracked separately in Weaver's
`docs/issues/stipulator-execution-deferral.md`. That approval does not establish
binding authoring, binding/pin hygiene, or requirement coverage. This issue tracks
the missing claim explicitly; external test evidence cannot substitute for it.

## Version evidence and attribution limits

Lightweight inspection on 2026-10-14 established:

- `go version` in Weaver reports
  `go version go1.27.2-X:nodwarf5 linux/amd64`.
- `command -v stipulator` resolves to `/home/nikolas/go/bin/stipulator`.
  `go version -m` on that file reports module version `v0.73.6`, built with
  `go1.27.1-X:nodwarf5`, revision
  `c01a2987170cb5610521e7b737e25cc2ffa2c5ce`, `vcs.modified=false`,
  `github.com/greatliontech/gofresh v0.112.1`, and
  `golang.org/x/tools v0.47.0`.
- The Stipulator checkout contains commit
  `bce1e185e8941e2dc47e39251d43094ced56a492`,
  `fix(structural): read Go 1.27.2 export data through the patched loader`.
  Its commit message describes V5 support for structural assertions and explicitly
  says separate root-tool adoption follows the gofresh release.

The on-disk CLI metadata is not proof of the running MCP process's identity.
A long-lived server can retain an older executable or dependency set; its build,
launch provenance, and effective resolver toolchain have not been established.
The structural-loader fix is likewise not proof that the installed MCP resolver
contains it or that its root-tool path supports the same exports. Do not attribute
this observation to a new defect in current source without that evidence.

Current source evidence: the root `go.mod` now selects Gofresh v0.117.5 and
the patched `go-x-tools` dependency. `internal/backends/golang/resolverclient.go`
and the MCP bind path still require identification of the actual live process;
these source dependencies do not establish its deployed export reader.

## Resolution requirements

Establish the actual MCP server/build and resolver toolchain provenance, then
determine whether compatible root-tool dependencies need deployment and a server
restart, or whether a reproducible compatibility defect remains in that identified
build. An unsupported export format should fail gracefully with an actionable
compatibility diagnostic and no partial binding writes, rather than only an
internal-importer error directing the user to report an issue.

Before closure, use the identified installed MCP server to resolve the current
toolchain exports and successfully author the intended `tests` claim for
`REQ-causal-membership` and the symbol above. Retain both outcomes with the same
server/build and toolchain provenance and confirm the binding was written.
A CLI-only success, a source commit, or records-verification deferral alone does
not meet the trigger. No patch, installation, restart, binding retry, or test run
is authorized by this filing.
