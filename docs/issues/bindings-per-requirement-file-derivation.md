# Bindings are placed by a layout the tool does not derive

`internal/author/author.go`'s `defaultBindingFile` places a new binding
in `.stipulator/bindings/<second id segment>.textproto` unless the claim
names a `--file`, and `dispose supersede` carries the successor's
bindings to that default with no file parameter at all (the MCP `dispose`
takes none). The gaps store already derives one file per requirement
(`records/author.go`: `gaps/<id lowercased, REQ- stripped>.textproto`),
so two record kinds spell their placement two ways, and a corpus that
wants its bindings laid out one file per requirement — the layout under
which two change sets never append to one file's tail — can hold it only
by a test the project writes and a hand move after every supersede.

Field report (greatliontech/cerebro, 2026-10-05): 2,769 records in four
segment files were split into 421 per-requirement files named by the
gaps derivation; the project's import-law package pins the layout; every
`bind` now passes `--file`, and a supersede's carried records are moved
by hand in the same change set.

What the tool could take, the choice being the tool's: derive a binding's
file from its requirement as the gaps store does — as the default, or as
a manifest option the project declares — so `bind` and `supersede` place
records where the layout says and the placement is unrepresentable
otherwise.

Lands: cross-tool train chunk 176 (the supersede carry's shape, where the
carry's placement is decided with its scope).
