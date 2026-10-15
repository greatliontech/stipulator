# Remaining workspace load, attribution and path judgments

Lands: tooling-redesign 7.8

`internal/backends/golang/workspace.go` parses workspace membership with
refusing errors; attribution has a separate workspace reading/error policy
in `depattribution.go`. `viewErrors` and dependency attribution still render
load states separately. Classification also obtains ownership and attribution
through parallel package-resolution paths. Preserve REQ-go-load-attribution's
states while deriving shared facts once where their scope agrees.

Two path residues remain distinct from those folds: `workspaceMembers` uses
host `filepath` syntax while policy validation also has a host-portable tree
path rule; `golang.go`'s ownership check uses `strings.HasPrefix(rel, "..")`,
which also excludes an in-tree `..foo` component. Cover malformed go.work,
portable spellings, escaping symlinks and an ordinary `..foo` member before
unifying the readers or changing their errors.

Complete selections, module mode, physical declaration positions, dotted
package identities and cross-view certificate dependencies are already
implemented. They are preservation constraints, not remaining limbs here.
Secondary constituent: 5.2e measures endpoint-local preparation before wider
load sharing; opening and closing observations remain independent.
