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

	var edReq []string
	editorial := &cobra.Command{
		Use:   "editorial",
		Short: guidanceShort("dispose editorial"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := oneFlag("req", edReq)
			if err != nil {
				return err
			}
			ups, err := author.Editorial(os.DirFS(chdir), req)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, ups)
		},
	}
	editorial.Flags().StringArrayVar(&edReq, "req", nil, "requirement identifier")
	registerReqCompletions(editorial, "req")

	var retireID []string
	var force bool
	retire := &cobra.Command{
		Use:   "retire",
		Short: guidanceShort("dispose retire"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := oneFlag("id", retireID)
			if err != nil {
				return err
			}
			ups, err := author.Retire(os.DirFS(chdir), id, force)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, ups)
		},
	}
	retire.Flags().StringArrayVar(&retireID, "id", nil, "retired identity (requirement id or term name)")
	retire.Flags().BoolVar(&force, "force", false, "retire even when no record names the identity")

	var from, into []string
	supersede := &cobra.Command{
		Use:     "supersede",
		Aliases: []string{"split", "merge"},
		Short:   guidanceShort("dispose supersede"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// These flags already express multiplicity, so a repetition
			// forms the batch: every occurrence's identifiers join
			// (REQ-evidence-claim-batch's batch arm) — none dropped.
			ups, err := author.Supersede(os.DirFS(chdir), splitLists(from), splitLists(into), false)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, ups)
		},
	}
	supersede.Flags().StringArrayVar(&from, "from", nil, "comma-separated source identifiers (removed from the spec; repeatable, occurrences join)")
	supersede.Flags().StringArrayVar(&into, "into", nil, "comma-separated successor identifiers (declaring supersedes; repeatable, occurrences join)")
	registerReqCompletions(supersede, "into")

	c.AddCommand(editorial, retire, supersede)
	return c
}

// splitLists joins every occurrence's comma-separated identifiers: the
// repetition arm of the batch contract for flags that already express
// multiplicity.
func splitLists(vals []string) []string {
	var out []string
	for _, v := range vals {
		out = append(out, splitList(v)...)
	}
	return out
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
