package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/remedy"
)

func retargetCmd() *cobra.Command {
	var backendVals, fromVals, toVals []string
	var check bool
	c := &cobra.Command{
		Use:   remedy.VerbRetarget,
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
			backends, closeBackends, err := makeBackends(cmd.Context(), chdir)
			if err != nil {
				return err
			}
			defer closeBackends()
			res, err := author.Retarget(os.DirFS(chdir), backends, backend, from, to)
			if err != nil {
				return err
			}
			for _, r := range res.Rows {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s -> %s\n", r.Requirement, r.Old, r.New)
			}
			for _, p := range res.Pointers {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  pointer `%s` -> `%s` in %s\n", p.Requirement, p.Old, p.New, p.Document)
			}
			for _, c := range res.Consented {
				fmt.Fprintln(cmd.OutOrStdout(), c)
			}
			if check {
				fmt.Fprintf(cmd.OutOrStdout(), "check only: %d binding(s)%s would retarget\n", len(res.Rows), res.PointerClause())
				return nil
			}
			if err := applyUpdates(chdir, res.Updates); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "retargeted %d binding(s)%s\n", len(res.Rows), res.PointerClause())
			return nil
		},
	}
	c.Flags().StringArrayVar(&backendVals, "backend", nil, "")
	c.Flags().StringArrayVar(&fromVals, "from", nil, "")
	c.Flags().StringArrayVar(&toVals, "to", nil, "")
	c.Flags().BoolVar(&check, "check", false, "")
	return c
}
