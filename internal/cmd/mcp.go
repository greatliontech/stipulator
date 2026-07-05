package cmd

import (
	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/mcpserver"
)

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the corpus and operations over MCP (stdio)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mcpserver.New(chdir).Run(cmd.Context())
		},
	}
}
