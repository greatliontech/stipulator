package cmd

import (
	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/backends/golang"
)

// internalResolveCmd is the owned resolver child's entry point: a parent
// stipulator process self-execs this subcommand and speaks the resolver
// protocol over its stdio, which puts go/packages symbol loading behind
// an owned, cancellable process boundary (REQ-go-owned-processes). It is
// hidden: process plumbing, never public CLI surface, and its argument
// is a verification-tree root the parent already resolved — not a
// corpus root, so root discovery is skipped for it.
func internalResolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:    golang.ResolverSubcommand + " <dir>",
		Short:  "Serve the owned symbol-resolution protocol on stdio (internal)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return golang.ServeResolver(cmd.Context(), args[0], cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}
