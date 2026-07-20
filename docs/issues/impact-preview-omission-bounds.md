# Impact preview: two REQ-change-impact omission bounds await a spec call

Lands: when the user disposes the REQ-change-impact spec-amend
candidates below (spec wins by default; both are user decisions).

## 1. Resolution tree unnamed — pure deletion is invisible code-side

The clause "those bound to symbols resolving into changed source files"
does not name the tree in which symbols resolve. The implementation
resolves in the worktree only, so a pure deletion previews no code-side
candidates: delete `leaf/leaf.go` (moving nothing) → "changed: 1 files",
zero bound hits, zero witness seeds — while spec-side deletions ARE
reported (the HEAD corpus compiles, so a removed requirement reads as
removed). Verify/check catch the resulting dangling bindings, and the
advisory posture covers the omission, but the asymmetry is unstated.

Either the clause names the worktree and owns the asymmetry, or HEAD-side
symbol resolution becomes required (a second package load of the
committed tree — cost sits with the user's call).

## 2. Non-implemented backends are silently outside the candidate set

Bindings whose backend the preview does not implement (today: everything
but "go") are skipped without any user-visible statement. The clause's
"bounded by what symbol resolution and import reach can see" arguably
covers it, but no output surface says "other backends not consulted". If
the bound is intended, stating it beside the import-reach bound would
make the omission clause complete.

## 3. go:embed couplings are outside reach but knowable

The package load requests `NeedImports` only, so a `go:embed` asset edit
seeds no package in the reach — although embeds are compile-time inputs
go/packages can name (`NeedEmbedFiles`), closer to an import edge than
to the clause's "runtime-input edit" example. Current behavior conforms
to "through the import graph"; widening reach to embeds is the same
spec call as the other two bounds.
