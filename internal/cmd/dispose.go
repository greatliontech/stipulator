package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/remedy"
	"github.com/greatliontech/stipulator/internal/verbcore"
)

func disposeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   remedy.VerbDispose,
		Short: "Apply a spec-change disposition to the records",
	}

	var edReq []string
	editorial := &cobra.Command{
		Use:   "editorial",
		Short: guidanceShort("dispose editorial"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := oneFlag("req", edReq)
			if err != nil {
				return err
			}
			ups, consented, err := author.Editorial(os.DirFS(chdir), req)
			if err != nil {
				return err
			}
			if err := applyUpdates(chdir, ups); err != nil {
				return err
			}
			for _, line := range consented {
				fmt.Printf("%s: %s\n", req, line)
			}
			return nil
		},
	}
	editorial.Flags().StringArrayVar(&edReq, "req", nil, "")
	registerReqCompletions(editorial, "req")

	var retireID []string
	var force bool
	retire := &cobra.Command{
		Use:   remedy.VerbDisposeRetire,
		Short: guidanceShort("dispose retire"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := oneFlag("id", retireID)
			if err != nil {
				return err
			}
			ups, err := author.Retire(os.DirFS(chdir), id, force)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, ups)
		},
	}
	retire.Flags().StringArrayVar(&retireID, remedy.FlagID, nil, "")
	retire.Flags().BoolVar(&force, remedy.FlagForce, false, "")

	var from, into []string
	var forceSupersede bool
	supersede := &cobra.Command{
		Use:     remedy.VerbDisposeSupersede,
		Aliases: []string{"split", "merge"},
		Short:   guidanceShort("dispose supersede"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// These flags already express multiplicity, so a repetition
			// forms the batch: every occurrence's identifiers join
			// (REQ-evidence-claim-batch's batch arm) — none dropped.
			fromIDs, err := verbcore.SplitIDLists(from)
			if err != nil {
				return err
			}
			intoIDs, err := verbcore.SplitIDLists(into)
			if err != nil {
				return err
			}
			ups, err := author.Supersede(os.DirFS(chdir), fromIDs, intoIDs, forceSupersede)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, ups)
		},
	}
	supersede.Flags().StringArrayVar(&from, remedy.FlagFrom, nil, "")
	supersede.Flags().StringArrayVar(&into, remedy.FlagInto, nil, "")
	supersede.Flags().BoolVar(&forceSupersede, remedy.FlagForce, false, "")
	registerReqCompletions(supersede, "into")

	c.AddCommand(editorial, retire, supersede)
	return c
}
