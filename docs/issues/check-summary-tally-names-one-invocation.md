# The check summary lacks a per-invocation witnessed account

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

1073 equals `dst-race`'s reported subject count, but this does not prove
that the scalar tally literally counts only that invocation. The current
`internal/verify.TestRun` account deduplicates subject identities; the
backend merges producing rows and the summary projects that aggregate.
Overlapping invocation populations can therefore produce this number.
The missing information is each invocation's eligibility and its own
served/executed/uncacheable disposition. The unwalked-selection wording
above belongs to the reported binary, not the current content-keyed audit.

Expected on the summary face (MCP UX doctrine: the minimum that
keeps the reader on point): the witnessed tally per invocation, or
one line per invocation stating its eligibility and its
served/executed/uncacheable split. The per-witness detail can stay
behind the views.

Lands: tooling-redesign 5.3

Retain the useful unique-subject aggregate but label its unit. Add a
per-invocation account from producing identity, including NoOutcome and
ineligible legs, without counting a freshness-served outcome as suite
health. Test unequal, overlapping invocation populations so equality with
one leg cannot conceal missing accounting.
