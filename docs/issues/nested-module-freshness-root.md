# Freshness Resolution Loses the Accepted Module Root

Lands: the next Go-backend resolution or freshness change exercising a test policy
with independently selected nested module roots.

## Observed failure

Weaver's accepted policy runs its root Go module and a separate consumer module:

```text
invocations {
  name: "protodb-consumer-race"
  timeout { seconds: 7200 }
  go {
    module_root: "consumers/protodb"
    packages: "./..."
    workspace_mode: GO_WORKSPACE_MODE_OFF
    race: true
    count: 1
    args: "-test.timeout=3m"
  }
}
```

The consumer tests execute successfully in their module, but resolution/freshness
preparation attempts to load their import path from the repository root:

```text
go list -json -deps -test -race github.com/thegrumpylion/weaver/consumers/protodb
exit status 1: no required module provides package
github.com/thegrumpylion/weaver/consumers/protodb
```

The resulting report marks the whole pass uncacheable, including independently
executed root-module witnesses. A completed Weaver run reported:

```text
witnessed: 1410 executed, 1410 uncacheable
1410 uncacheable: freshness path degraded: go list -json -deps -test -race
github.com/thegrumpylion/weaver/consumers/protodb: exit status 1:
no required module provides package ...
```

The observed binary is Stipulator v0.73.6, revision
`c01a2987170cb5610521e7b737e25cc2ffa2c5ce`, with gofresh v0.112.1, built using
`go1.27.1-X:nodwarf5`. This identity comes from `go version -m`, not an assumption
that the installed executable equals the current checkout.

## Reproduction and diagnosis boundary

Use a root module with its own tests, plus an independent nested module containing
tests and a distinct module path. Do not add that module as a root dependency or
introduce a workspace. Declare both in accepted policy with the nested invocation's
`module_root` and workspace mode above; bind representative witnesses from both.
Run `mlock run stipulator check --full` from the repository root, then inspect
resolution and freshness diagnostics. A small two-module fixture is sufficient;
replaying Weaver's full application suite is not required to investigate this.

Execution understands the invocation module selection. The resolution/freshness
path must retain it too. Relevant source inspection points are
`internal/backends/golang/served.go` (resolver child directory), `derive.go`
(engine construction and recorder preparation), and `witnessrun.go` (selective
preparation/degraded execution). At checkout
`e20fea2e419b995937025b3f30abb3d780d8c3f1`, these paths pass the verification/tree
root where a nested invocation needs its owning module root. Preparation failure
can clear all freshness groups, expanding the effect beyond the failing module.
This source trace is an investigation lead, not a claim of a completed fix.

This issue concerns lost freshness and its broad degradation, not incorrect test
execution or cached false passes. `check --full` deliberately executes the complete
accepted policy even with valid freshness evidence. Fixing this bug will not make
that mode selective. Policy `count: 1` disables Go's test-result cache, not
Stipulator's evidence reuse. Weaver also has a separate application-side journal
I/O amplification problem; its long test runtimes must not be attributed wholly
to this defect.

## Required outcome

- Resolve, capture and publish each witness under the accepted owning module root,
  workspace mode and effective build selection.
- Never substitute root dependencies, a workspace, purity attestations, or omitted
  integration tests to make the failing lookup disappear.
- Establish whether failure containment can preserve unrelated root-module
  evidence without accepting incomplete inputs; refusal remains preferable to an
  unjustified freshness certificate.
- Exercise both full execution and selective checks on a small multi-module
  fixture, including changes within either module and invalid module selections.
- After correction, other honest uncacheability causes may remain. Do not claim
  universal reuse merely because the directory-resolution failure is fixed.
