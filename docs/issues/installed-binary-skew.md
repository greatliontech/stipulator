# Installed binary skewed from the tree (fleet sweep 2026-10-12)

The weekly fleet sweep's binary-provenance check reports the installed
`stipulator` (`~/go/bin/stipulator`) stamped
`vcs.revision=372ef9933bb5` while the repo's last build-input commit
is `55e7e5ec2ccd`: SKEW. The binary anchors to a commit (no modified
flag), but the tree has taken build-input commits since it was built,
so the installed tool is not the tree — and the installed binary is
what the sweep's own `stipulator check` runs over every estate with,
so its skew is read fleet-wide. The check is deliberately coarse —
any commit moves HEAD, docs included — and the standing response is
equally cheap: `go install` in this repo from a clean tree (committed
HEAD, `git status` empty), then restart long-lived readers
(`stipulator mcp`), then close. The sweep's next provenance line
reading `match` is the check.

Scanned: the index and all 33 docs it lists. None touches the
installed binary's provenance (cli-test-binary-builds concerns the
test fixtures building a binary, not the installed one). Rows
disjoint; no scheduled trigger duplicated.

Lands: tooling-redesign 5.2 (cross-tool train chunk 342, stipulator's
next chunk in the active sequence) — its close-out `go install` from
the committed tree; the sweep's next provenance line reading `match`
closes it, and an earlier clean install closes it sooner.
