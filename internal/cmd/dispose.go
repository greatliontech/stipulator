package cmd

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
)

func disposeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "dispose",
		Short: "Apply a spec-change disposition to the records",
	}

	var edReq string
	editorial := &cobra.Command{
		Use:   "editorial",
		Short: "Re-pin a requirement's bindings after a meaning-preserving edit",
		RunE: func(cmd *cobra.Command, args []string) error {
			ups, err := author.Editorial(os.DirFS(chdir), edReq)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, ups)
		},
	}
	editorial.Flags().StringVar(&edReq, "req", "", "requirement identifier")
	registerReqCompletions(editorial, "req")

	var retireID string
	var force bool
	retire := &cobra.Command{
		Use:   "retire",
		Short: "Tombstone an identity removed from the spec; delete its records",
		RunE: func(cmd *cobra.Command, args []string) error {
			ups, err := author.Retire(os.DirFS(chdir), retireID, force)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, ups)
		},
	}
	retire.Flags().StringVar(&retireID, "id", "", "retired identity (requirement id or term name)")
	retire.Flags().BoolVar(&force, "force", false, "retire even when no record names the identity")

	var from, into string
	supersede := &cobra.Command{
		Use:     "supersede",
		Aliases: []string{"split", "merge"},
		Short:   "Tombstone sources and retarget their bindings to declaring successors",
		RunE: func(cmd *cobra.Command, args []string) error {
			ups, err := author.Supersede(os.DirFS(chdir), splitList(from), splitList(into), false)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, ups)
		},
	}
	supersede.Flags().StringVar(&from, "from", "", "comma-separated source identifiers (removed from the spec)")
	supersede.Flags().StringVar(&into, "into", "", "comma-separated successor identifiers (declaring supersedes)")
	registerReqCompletions(supersede, "into")

	c.AddCommand(editorial, retire, supersede)
	return c
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
