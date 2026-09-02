package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
)

func attestCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "attest",
		Short: "Record a human judgment as a requirement's weakest evidence",
	}
	c.AddCommand(attestRequirementCmd())
	return c
}

func attestRequirementCmd() *cobra.Command {
	var reqVals, reasonVals []string
	var retract bool
	c := &cobra.Command{
		Use:   "requirement",
		Short: guidanceShort("attest requirement"),
		Long:  guidanceHelp("attest requirement"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := oneFlag("req", reqVals)
			if err != nil {
				return err
			}
			reason, err := oneFlag("reason", reasonVals)
			if err != nil {
				return err
			}
			if retract {
				up, prior, err := author.RetractAttestation(os.DirFS(chdir), req)
				if err != nil {
					return err
				}
				if err := applyUpdates(chdir, []author.Update{*up}); err != nil {
					return err
				}
				fmt.Printf("retracted %s (was: %q)\n", req, prior.GetReason())
				return nil
			}
			up, prior, err := author.AttestRequirement(os.DirFS(chdir), req, reason)
			if err != nil {
				return err
			}
			// The write rides the CAS applier like every other record
			// write: the Update's prior stamp is checked, so a
			// concurrent agent's judgment refuses instead of being
			// silently overwritten (REQ-record-cas).
			if err := applyUpdates(chdir, []author.Update{*up}); err != nil {
				return err
			}
			if prior != nil {
				fmt.Printf("replaced judgment (was: %q)\n", prior.GetReason())
			}
			return nil
		},
	}
	c.Flags().StringArrayVar(&reqVals, "req", nil, "requirement identifier")
	c.Flags().StringArrayVar(&reasonVals, "reason", nil, "why the requirement is judged satisfied")
	c.Flags().BoolVar(&retract, "retract", false, "withdraw the requirement's judgment instead of authoring one")
	registerReqCompletions(c, "req")
	return c
}
