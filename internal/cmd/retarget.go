package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
)

func retargetCmd() *cobra.Command {
	var backend, from, to string
	var check bool
	c := &cobra.Command{
		Use:   "retarget",
		Short: guidanceShort("retarget"),
		Long:  guidanceLong("retarget"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			backends, err := makeBackends(cmd.Context(), chdir)
			if err != nil {
				return err
			}
			ups, rows, err := author.RetargetSymbols(os.DirFS(chdir), backends, backend, from, to)
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s -> %s\n", r.Requirement, r.Old, r.New)
			}
			if check {
				fmt.Fprintf(cmd.OutOrStdout(), "check only: %d binding(s) would retarget\n", len(rows))
				return nil
			}
			if err := applyUpdates(chdir, ups); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "retargeted %d binding(s)\n", len(rows))
			return nil
		},
	}
	c.Flags().StringVar(&backend, "backend", "go", "backend whose symbols retarget")
	c.Flags().StringVar(&from, "from", "", "old symbol prefix (module path)")
	c.Flags().StringVar(&to, "to", "", "new symbol prefix")
	c.Flags().BoolVar(&check, "check", false, "report affected identities without writing")
	return c
}
