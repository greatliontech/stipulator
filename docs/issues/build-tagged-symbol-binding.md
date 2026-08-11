# Build-tagged symbols are unresolvable at binding time

## Problem

Binding-side symbol resolution is tag-blind while execution-side
discovery is tag-aware, so a test that exists only under a build tag
can be *run* by the accepted policy but never *bound* as a witness.

- Execution: `listPackages` passes the invocation's tags to `go list`
  (`internal/backends/golang/discovery.go:161`), so a policy
  invocation like `{name: "dst", tags: "dst", toolchain: ...}`
  discovers and runs tag-gated tests as suite-health obligations.
- Binding: `newContext` loads the tree via `packages.Load` with no
  build flags (`internal/backends/golang/golang.go:47-64`), so a
  symbol declared in a `//go:build dst` file resolves `NotFound` in
  `Resolve` — it cannot carry `BINDING_ROLE_TESTS`, has no shape
  hash, and cannot serve as a requirement's witness.

The consequence in the field (tugboat): the deterministic-simulation
leg carries the *strongest* enforcement of several for-all
requirements (crash schedules, torn-fence arcs), yet those tests are
invisible to the binding corpus. Gaps whose planned enforcement is a
DST arm can never retire "with enforcement bound":

- `REQ-node-support-stability`'s dst-schedule witness gap (already
  recorded against this capability).
- tugboat's lifecycle program, chunk 12: four invariant-class gaps
  (authority-first, cut-completeness, fence-terminal, epoch-residue)
  whose property-class enforcement is the composed-fence DST sweep's
  arms — the plan's close-out is unsatisfiable without this.

## Direction

Resolution loads one package view per distinct build selection in the
accepted policy (the set of tag-sets across invocations, deduped; the
default no-tag view stays), and `Resolve` consults all views —
first-declaring view wins and supplies the shape hash. The policy is
already the authority on which build selections exist, so no new
configuration surface is needed. Freshness/impact machinery must key
witness evidence to the view that declared the symbol so a tag-gated
file edit invalidates the right witnesses.

## Lands

Cross-tool train chunk 84 (gofresh `docs/plans/cross-tool-train.md`).
Deadline pressure: needed before tugboat's lifecycle chunk 12 opens
its close-out (the four-gap retirement above).
