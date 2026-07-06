package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/corpus"
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scaffold the manifest for a new corpus",
		RunE: func(cmd *cobra.Command, args []string) error {
			up, err := author.Init(os.DirFS(chdir))
			if err != nil {
				return err
			}
			if err := applyUpdates(chdir, []author.Update{*up}); err != nil {
				return err
			}
			fmt.Printf("initialized: corpus is %s\n", corpus.DefaultInclude)
			fmt.Println(dim("next: write a spec document, then `stipulator compile`"))
			return nil
		},
	}
}
