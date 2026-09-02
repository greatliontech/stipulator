package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
)

func retargetCmd() *cobra.Command {
	var backendVals, fromVals, toVals []string
	var check bool
	c := &cobra.Command{
		Use:   "retarget",
		Short: guidanceShort("retarget"),
		Long:  guidanceHelp("retarget"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := oneFlag("backend", backendVals)
			if err != nil {
				return err
			}
			// Only an ABSENT flag defaults: an explicit --backend ""
			// flows through to be refused downstream, exactly as bind
			// treats it — silence coerced to a default would guess.
			if len(backendVals) == 0 {
				backend = "go"
			}
			from, err := oneFlag("from", fromVals)
			if err != nil {
				return err
			}
			to, err := oneFlag("to", toVals)
			if err != nil {
				return err
			}
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
	c.Flags().StringArrayVar(&backendVals, "backend", nil, "backend whose symbols retarget (default go)")
	c.Flags().StringArrayVar(&fromVals, "from", nil, "old symbol prefix (module path)")
	c.Flags().StringArrayVar(&toVals, "to", nil, "new symbol prefix")
	c.Flags().BoolVar(&check, "check", false, "report affected identities without writing")
	return c
}
