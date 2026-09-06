# Go backend load path: parallel mechanisms in workspace membership, error attribution, and classification

Three pairs on the package-load path each state one judgment twice:

- `workspaceMembers` (`internal/backends/golang/workspace.go`) parses
  the workspace twice under two error policies — once to enumerate,
  once to attribute — so a malformed `go.work` reaches two different
  refusal texts depending on which parse sees it first.
- The load-failure vocabularies: the `viewErrors` wording and the
  dependency-resolution attribution (`depattribution.go`) name the
  same states with different words; `moduleOwns` collapsed the xtest
  half of this but the wording split remains.
- The classifier resolves a package twice (once for ownership, once
  for the attribution state) where one resolution feeds both.

One load result — membership, ownership, attribution state — computed
once per package and consumed by every reader would collapse the
three; REQ-go-load-attribution's stated states are the invariant to
preserve.

Lands: with the next change set touching the Go backend's package
load path (`workspace.go`, `depattribution.go`, or the view-error
attribution).
