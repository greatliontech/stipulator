package cmd

import (
	"os"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
)

func gapCmd() *cobra.Command {
	var req, reason, coveredID, existsID, attested string
	c := &cobra.Command{
		Use:   "gap",
		Short: "Declare a coverage gap with a landing condition",
		RunE: func(cmd *cobra.Command, args []string) error {
			g := &stipulatorv1.Gap{}
			g.SetRequirementId(req)
			g.SetReason(reason)
			lc, err := author.NewLandingCondition(coveredID, existsID, attested)
			if err != nil {
				return err
			}
			if lc != nil {
				g.SetLands(lc)
			}
			up, err := author.Gap(os.DirFS(chdir), g)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, []author.Update{*up})
		},
	}
	c.Flags().StringVar(&req, "req", "", "requirement identifier")
	c.Flags().StringVar(&reason, "reason", "", "why the gap exists")
	c.Flags().StringVar(&coveredID, "covered", "", "lands when this requirement is covered")
	c.Flags().StringVar(&existsID, "exists", "", "lands when this requirement exists")
	c.Flags().StringVar(&attested, "attested", "", "lands on this external condition, fired explicitly")
	registerReqCompletions(c, "req", "covered", "exists")
	return c
}
