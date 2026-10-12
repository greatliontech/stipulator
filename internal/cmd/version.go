package cmd

import (
	"fmt"

	"github.com/greatliontech/gofresh/toolversion"
	"github.com/spf13/cobra"
)

func versionString() string { return "stipulator " + toolversion.Identity() }

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: guidanceShort("version"), Long: guidanceHelp("version"),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), versionString())
			return err
		},
	}
}

// versionQuery uses Cobra's command and flag grammar so a value named version
// (for example a --chdir operand) cannot bypass a real command's preparation.
func versionQuery(root *cobra.Command, args []string) bool {
	root.InitDefaultVersionFlag()
	selected, remaining, err := root.Find(args)
	if err != nil {
		return false
	}
	if selected.Name() == "version" && selected.Parent() == root {
		return true
	}
	if selected != root || root.ParseFlags(remaining) != nil {
		return false
	}
	value, err := root.Flags().GetBool("version")
	return err == nil && value
}
