# diff --against refuses linked git worktrees

Lands: when diff-against-revision is needed from a linked worktree.

`gitfs.FS` refuses a corpus reached through a `.git` file — a
`git worktree add` checkout — with "run from the main worktree". The
refusal is deliberate fail-loud: the embedded git (go-git v5) opens the
gitfile redirection but does not wire the commondir object and reference
stores, so every revision — HEAD, branches, raw hashes — resolves as
"reference not found", an error that blames the caller's revision rather
than the environment.

Real support means resolving the commondir indirection (gitdir file →
worktree gitdir → commondir) and opening the object store there — either
upstream go-git support or a small commondir-aware opener here. Until a
consumer works primarily from linked worktrees, the refusal is the
correct behavior.
