# The check summary's witnessed tally covers one invocation and does not say which

Field report from tugboat (filed uncommitted in this tree, the
standing channel), 2026-09-29, stipulator e9254e7.

tugboat's accepted policy carries three invocations over the same
packages: `dst` (`-tags dst`), `dst-race` (`-tags dst -race`), and
`race` (`-race`). The check's summary face prints:

    executing dst-race: 1073 subjects in 18 packages (16.9s)
    executing race: 1023 subjects in 18 packages (16.9s)
    executing dst: 1069 subjects in 18 packages — ineligible leg of shared packages, failures only, never a witness outcome (16.9s)
    ...
    invocation "dst-race": toolchain-selection audit: selection "dst,race" under go1.27.0-dst.14 is unwalked — ...
    resolution: 1119 served from records, 75 resolved typed
    witnessed: 0 served fresh, 1073 executed, 1073 uncacheable

1073 is exactly `dst-race`'s subject count. The `race` invocation's
1023 subjects were executed (the progress lines show all 18 packages)
but appear nowhere in the witnessed tally, and the face never says
whether they were witness-ineligible like `dst`'s (which IS labeled
so), judged and served, or judged and uncacheable under their own
reasons. For a consumer whose one selection listed in gofresh's
toolchain audit is `race` (`dst` and `dst,race` are unwalked under
go1.27.0-dst.14), the `race` leg is the only one that can serve
today — and the summary does not show whether it does.

Expected on the summary face (MCP UX doctrine: the minimum that
keeps the reader on point): the witnessed tally per invocation, or
one line per invocation stating its eligibility and its
served/executed/uncacheable split. The per-witness detail can stay
behind the views.

Lands: a rider on cross-tool train chunk 249 (gofresh
docs/plans/cross-tool-train.md — check.Run's ladder; triaged at gofresh chunk 289's record,
2026-09-29): the witnessed tally per invocation, one line per invocation
stating its eligibility and its served/executed/uncacheable split — the
never-silent rule.
