package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
)

func gapCmd() *cobra.Command {
	var reqs []string
	var reason, coveredID, existsID, manual string
	var fired, retract bool
	c := &cobra.Command{
		Use:   "gap",
		Short: "Declare, fire, or retract a coverage gap",
		Long: "Declares coverage gaps with a landing condition (--req repeatable; all\n" +
			"share the reason and condition; --covered self lands each requirement on\n" +
			"its own coverage). --fired alone marks existing gaps' manual conditions\n" +
			"fired — the external judgment entering through the validated path.\n" +
			"--retract deletes gap records — dangling records included: retraction is\n" +
			"the dangling state's repair, and it never touches the tombstone registry.\n" +
			"Batches apply all-or-nothing.",
		RunE: func(cmd *cobra.Command, args []string) error {
			conditioned := coveredID != "" || existsID != "" || manual != "" || reason != ""
			switch {
			case retract:
				if conditioned || fired {
					return fmt.Errorf("--retract takes only --req: retraction deletes the record, conditions do not apply")
				}
				ups, err := author.RetractGaps(os.DirFS(chdir), reqs)
				if err != nil {
					return err
				}
				return applyUpdates(chdir, ups)
			case fired && manual == "":
				if conditioned {
					return fmt.Errorf("--fired alone fires existing gaps; declaring a new fired gap takes --manual with --fired")
				}
				ups, err := author.FireGaps(os.DirFS(chdir), reqs)
				if err != nil {
					return err
				}
				return applyUpdates(chdir, ups)
			}
			lc, err := author.NewLandingCondition(coveredID, existsID, manual, fired)
			if err != nil {
				return err
			}
			ups, notes, err := author.Gaps(os.DirFS(chdir), reqs, reason, lc)
			if err != nil {
				return err
			}
			for _, n := range notes {
				fmt.Println(n)
			}
			return applyUpdates(chdir, ups)
		},
	}
	c.Flags().StringArrayVar(&reqs, "req", nil, "requirement identifier (repeatable; all share the reason and landing condition)")
	c.Flags().StringVar(&reason, "reason", "", "why the gap exists")
	c.Flags().StringVar(&coveredID, "covered", "", "lands when this requirement is covered (self = each requirement's own coverage)")
	c.Flags().StringVar(&existsID, "exists", "", "lands when this requirement exists")
	c.Flags().StringVar(&manual, "manual", "", "lands on this externally judged condition, fired explicitly")
	c.Flags().BoolVar(&fired, "fired", false, "mark the manual condition fired (alone: fire existing gaps)")
	c.Flags().BoolVar(&retract, "retract", false, "delete the gap records instead of declaring (dangling records included)")
	registerReqCompletions(c, "req", "covered", "exists")
	return c
}
