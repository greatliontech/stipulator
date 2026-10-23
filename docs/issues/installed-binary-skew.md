# Installed binary trails the repo's last build input (fleet sweep 2026-10-19)

The 2026-10-19 weekly fleet sweep's binary-provenance check reported the
installed `stipulator` stamped `vcs.revision=1f90e1479658` while the repo's
last build-input commit is `a54b50c2d793`: SKEW. The binary anchors cleanly
to a commit (no modified flag), so its content is recoverable from history,
but the tree has since taken further build-input commits. Every
`stipulator` invocation on this machine — the sweep's own check verdicts and
the MCP server sessions among them — therefore runs code older than HEAD's,
and a verdict it renders cannot be attributed to the current tree. The skew
guards refuse loudly at use; the sweep catches the drift before use.

The remedy is the standing one: `go install` from the committed tree, which
moves the provenance line back to `match`. Installation covers the binary
file; an already-running MCP server keeps the old code until it restarts,
so closure needs actual file and reader identity, not an inferred restart.
This filing records the fact and performs no install.

The 2026-10-12 sweep's skew filing (372ef9933bb5 against 55e7e5ec2ccd)
closed at a later clean install; this is the same drift class reopened by
later commits, not that fact persisting.

Lands: tooling-redesign 5.2e — the migration's commit, installation and
reader-restart step, which is stipulator's next scheduled clean install. An
earlier `go install` from the committed HEAD closes it sooner; the closing
observation is the next sweep's provenance line reading `match`.
