package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
)

func attestCmd() *cobra.Command {
	var symbol, position, operator, reason string
	c := &cobra.Command{
		Use:   "attest",
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
