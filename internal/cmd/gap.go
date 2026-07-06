package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
)

func gapCmd() *cobra.Command {
	var reqs []string
	var reason, coveredID, existsID, manual string
	c := &cobra.Command{
		Use:   "gap",
		Short: "Declare a coverage gap with a landing condition",
		RunE: func(cmd *cobra.Command, args []string) error {
			lc, err := author.NewLandingCondition(coveredID, existsID, manual)
			if err != nil {
				return err
			}
			ups, err := author.Gaps(os.DirFS(chdir), reqs, reason, lc)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, ups)
		},
	}
	c.Flags().StringArrayVar(&reqs, "req", nil, "requirement identifier (repeatable; all share the reason and landing condition)")
	c.Flags().StringVar(&reason, "reason", "", "why the gap exists")
	c.Flags().StringVar(&coveredID, "covered", "", "lands when this requirement is covered")
	c.Flags().StringVar(&existsID, "exists", "", "lands when this requirement exists")
	c.Flags().StringVar(&manual, "manual", "", "lands on this externally judged condition, fired explicitly")
	registerReqCompletions(c, "req", "covered", "exists")
	return c
}
