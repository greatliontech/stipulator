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
	var req, reason string
	var retract bool
	c := &cobra.Command{
		Use:   "requirement",
		Short: "Record the weakest evidence: a reason-carrying voucher for a requirement",
		Long: "Author an attestation record — the bottom of the evidence ladder. It counts\n" +
			"only where the manifest policy names attestation as a cell's minimum, renders\n" +
			"as its own coverage bucket (never folded into covered), and re-stales when\n" +
			"the requirement's text moves.",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if err := writeFileAt(chdir, up.Path, up.Content); err != nil {
				return err
			}
			if prior != nil {
				fmt.Printf("replaced judgment (was: %q)\n", prior.GetReason())
			}
			fmt.Printf("wrote %s\n", up.Path)
			return nil
		},
	}
	c.Flags().StringVar(&req, "req", "", "requirement identifier")
	c.Flags().StringVar(&reason, "reason", "", "why the requirement is judged satisfied")
	c.Flags().BoolVar(&retract, "retract", false, "withdraw the requirement's judgment instead of authoring one")
	registerReqCompletions(c, "req")
	return c
}
