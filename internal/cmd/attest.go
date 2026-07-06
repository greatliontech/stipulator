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
		Short: "Record a human judgment: a surviving mutant's equivalence, or a requirement's weakest evidence",
	}
	c.AddCommand(attestSurvivorCmd(), attestRequirementCmd())
	return c
}

func attestSurvivorCmd() *cobra.Command {
	var symbol, position, operator, reason string
	c := &cobra.Command{
		Use:   "survivor",
		Short: "Disposition a surviving mutant as attested equivalent",
		Long: "Record on the kill-sheet that a surviving mutant is equivalent (or accepted),\n" +
			"with the reasoning. The attestation rides the sheet's pins: a changed body,\n" +
			"witness set, or operator set sheds it, so every version is re-judged.",
		RunE: func(cmd *cobra.Command, args []string) error {
			up, err := author.Attest(os.DirFS(chdir), symbol, position, operator, reason)
			if err != nil {
				return err
			}
			if err := writeFileAt(chdir, up.Path, up.Content); err != nil {
				return err
			}
			fmt.Printf("wrote %s\n", up.Path)
			return nil
		},
	}
	c.Flags().StringVar(&symbol, "symbol", "", "mutated symbol whose sheet carries the survivor")
	c.Flags().StringVar(&position, "position", "", "survivor position, as printed by harden (file.go:line:col)")
	c.Flags().StringVar(&operator, "operator", "", "survivor operator, as printed by harden")
	c.Flags().StringVar(&reason, "reason", "", "why the mutant is equivalent or accepted")
	return c
}

func attestRequirementCmd() *cobra.Command {
	var req, reason string
	c := &cobra.Command{
		Use:   "requirement",
		Short: "Record the weakest evidence: a reason-carrying voucher for a requirement",
		Long: "Author an attestation record — the bottom of the evidence ladder. It counts\n" +
			"only where the manifest policy names attestation as a cell's minimum, renders\n" +
			"as its own coverage bucket (never folded into covered), and re-stales when\n" +
			"the requirement's text moves.",
		RunE: func(cmd *cobra.Command, args []string) error {
			up, err := author.AttestRequirement(os.DirFS(chdir), req, reason)
			if err != nil {
				return err
			}
			if err := writeFileAt(chdir, up.Path, up.Content); err != nil {
				return err
			}
			fmt.Printf("wrote %s\n", up.Path)
			return nil
		},
	}
	c.Flags().StringVar(&req, "req", "", "requirement identifier")
	c.Flags().StringVar(&reason, "reason", "", "why the requirement is judged satisfied")
	registerReqCompletions(c, "req")
	return c
}
