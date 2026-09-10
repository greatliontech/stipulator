package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/prune"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/remedy"
)

func pruneCmd() *cobra.Command {
	var check, noTest, dangling, storeGC bool
	c := &cobra.Command{
		Use:   remedy.VerbPrune,
		Short: guidanceShort("prune"),
		Long:  guidanceHelp("prune"),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := prune.Mode{Check: check, NoTest: noTest, Dangling: dangling, Store: storeGC}
			if err := mode.Validate(); err != nil {
				return err
			}
			deps := prune.Deps{
				Deps:    cliDeps(),
				Root:    chdir,
				Compile: func() (*stipulatorv1.Spec, error) { return mustCompile(chdir) },
				Load:    func() (*records.Store, error) { return records.Load(os.DirFS(chdir)) },
			}
			if mode.Store {
				res, err := prune.StoreGC(cmd.Context(), deps)
				if err != nil {
					return withRecordPath(err)
				}
				fmt.Printf("store gc: %d record variant(s) removed, %d kept\n", res.Removed, res.Kept)
				if res.Resolutions != nil {
					fmt.Printf("store gc: %d resolution record(s) removed, %d kept\n", res.Resolutions.Removed, res.Resolutions.Kept)
				}
				return nil
			}
			if mode.Dangling {
				prunes, err := prune.Dangling(deps)
				if err != nil {
					return err
				}
				if check {
					for _, up := range prunes {
						fmt.Printf("%s dangling gap lingers: %s\n", yellow("prunable:"), up.Path)
					}
					if len(prunes) > 0 {
						return fmt.Errorf("prune --dangling --check: %d dangling gaps linger", len(prunes))
					}
					fmt.Println(green("prune: no dangling gaps"))
					return nil
				}
				if err := applyUpdates(chdir, prunes); err != nil {
					return err
				}
				fmt.Printf("prune: %d dangling gaps deleted\n", len(prunes))
				return nil
			}
			res, err := prune.Evaluate(cmd.Context(), deps, noTest)
			if err != nil {
				var pe *prune.ProblemsError
				if errors.As(err, &pe) {
					return refuseHygiene(pe.Problems)
				}
				return withRecordPath(err)
			}
			if !res.Evaluated {
				if check {
					fmt.Println(green("prune: clean"))
					return nil
				}
				fmt.Println("prune: no gap records - nothing to evaluate")
				return nil
			}
			fmt.Fprintln(os.Stderr, dim(res.Line()))
			if check {
				for _, up := range res.Prunes {
					fmt.Printf("%s resolved gap lingers: %s\n", yellow("prunable:"), up.Path)
				}
				if len(res.Prunes) > 0 {
					return fmt.Errorf("prune --check: %d resolved gaps linger", len(res.Prunes))
				}
				fmt.Println(green("prune: clean"))
				return nil
			}
			if err := applyUpdates(chdir, res.Prunes); err != nil {
				return err
			}
			fmt.Printf("prune: %d resolved gaps pruned\n", len(res.Prunes))
			return nil
		},
	}
	c.Flags().BoolVar(&check, "check", false, "")
	c.Flags().BoolVar(&noTest, "no-test", false, "")
	c.Flags().BoolVar(&dangling, remedy.FlagDangling, false, "")
	c.Flags().BoolVar(&storeGC, "store", false, "")
	return c
}
