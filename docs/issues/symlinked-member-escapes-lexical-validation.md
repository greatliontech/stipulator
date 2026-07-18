# A symlinked workspace member escapes lexical tree validation

Lands: when workspace member and module-root validation resolves symlinks, or
when execution refuses a member whose resolved path leaves the tree.

## Observed

Workspace member and policy module-root validation is lexical: an in-tree
relative path that is itself a symlink to a directory outside the tree passes
every escape check, and execution later operates outside the verification
tree. The same lexical posture predates the policy record in the legacy
workspace member enumeration.

## Resolution

Resolve candidate paths before the escape check, or refuse execution when a
member's resolved location is not under the resolved tree root.
