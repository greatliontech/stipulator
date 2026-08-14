# Tagged package views load under the ambient toolchain — dst view unloadable, context fails whole

## Problem

Chunk 84's per-selection resolution loads one package view per policy
build selection, passing the selection's TAGS (`newContext`,
`internal/backends/golang/golang.go:69-84`: `-tags=` in BuildFlags) —
but not its TOOLCHAIN. `policyBuildSelections` extracts
`inv.GetGo().GetTags()` only (`golang.go:132`); the load env is the
ambient `goworkEnv(dir)`. A policy invocation shaped
`{name: "dst", tags: "dst", toolchain: <godst bin/go>}` therefore
loads the dst view under the DEFAULT toolchain, where the godst-only
stdlib surface does not exist.

Field failure (tugboat, first bind after 84):

    claim 1 (REQ-lc-authority-first github.com/greatliontech/tugboat/lifecycle.TestDSTComposedFenceSweep):
    resolving ...: package github.com/greatliontech/tugboat/lifecycle_test
    has load errors: lifecycle/dstbubble_test.go:38:2: could not import
    testing/simulation (invalid package name: "")

Blast radius: the load error fails `newContext` whole — EVERY bind on
the repo now errors, not just dst-tagged claims. Pre-84 the same
claims refused NotFound with the rest of binding healthy, so a repo
with a toolchain'd tag selection is net worse off until this lands.

## Direction (need-level)

Each selection's view loads under its invocation's toolchain — the
policy already carries it next to the tags; the loader's
`packages.Config.Env` (and/or PATH/GOROOT) derives from the same
invocation the tags came from. Freshness keying presumably follows
(a view's identity is tags+toolchain, not tags alone). Whether an
unloadable single view should degrade that view rather than fail the
context is the tool's design call — but the pre-84 healthy-remainder
behavior is the floor to restore either way.

## Lands

Fold into the current train work — the consumer (tugboat lifecycle
close-out) is at this gate NOW, blocked on exactly this: six gap
retirements wait on binding dst-tagged witnesses.

**BLOCKING NOW (2026-08-14).**
