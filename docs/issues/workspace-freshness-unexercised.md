# Workspace trees have no freshness test and fail silent-slow

Lands: when a go.work tree is next witnessed in anger, or when the
freshness path's engine construction next changes.

## Context

The freshness path builds one gofresh engine at the tree root. On a
`go.work` tree, if the engine cannot resolve member-module import paths,
every fingerprint check errors (stale) and every capture errors (uncached):
the gate re-runs the full suite forever while reporting only "0 served
fresh", with nothing saying why. The failure direction is safe — spurious
re-runs, never spurious reuse — but it is a silent cost cap: the feature
quietly never engages. No stipulator test runs the freshness path over a
workspace fixture (the existing workspace fixtures exercise the full-run
witness path only).

## Resolution

A workspace fixture through RunTestsFresh asserting second-run serves; or,
if member resolution genuinely cannot work, an explicit degrade to the full
run with the stderr note naming the cause.
