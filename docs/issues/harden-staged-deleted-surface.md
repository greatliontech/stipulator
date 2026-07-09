# staged-diff coarsely labels deleted and unloadable surfaces

Lands: when `targets --staged-diff` gains a disposition distinguishing a deleted
or unloadable surface from an integration seam.

`targets --staged-diff` classifies a changed `.go` path that is absent from the
loaded packages — a deletion, a file behind a non-matching `//go:build` tag,
or a file in a package that failed to load — as `integration-seam` (a changed
file declaring no mutatable body). For a deletion this conflates "a covered
implementation was removed" with "an edit outside any body": `gitfs.Changed`
reports the deleted path, `golang.Backend.Surface` finds no loaded symbols for
it, and the classifier reads the empty symbol set as an integration seam.

The report is advisory, never a gate, so this misleads rather than breaks. The
precise signal — a bound `implements` symbol vanished, so its requirement's
coverage dropped — is the coverage-delta reminder's job (chunk 2), which
compares covered surfaces against a base and knows when an implementation was
removed. Fold the deleted/unloadable distinction in there, or have the
staged-diff report mark a deletion of a previously-bound symbol as its own
disposition rather than an integration seam.
