package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

func pruneCmd() *cobra.Command {
	var check, noTest bool
	c := &cobra.Command{
		Use:   "prune",
		Short: "Delete resolved gap records",
		Long: "Deletes gap records whose requirements have reached the covered bucket —\n" +
			"a resolved gap is satisfied, dead record weight. --check lints instead:\n" +
			"non-zero exit when any resolved gap lingers, deleting nothing.",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(chdir)
			if err != nil {
				return err
			}
			fsys := os.DirFS(chdir)
			store, err := records.Load(fsys)
			if err != nil {
				return err
			}
			var testRun *verify.TestRun
			if !noTest {
				fmt.Fprintln(os.Stderr, dim("witnessing: fresh-checked; stale and unproven tests run (-race)"))
				if testRun, err = golang.RunTestsFresh(chdir); err != nil {
					return err
				}
				if testRun.Ran+testRun.Fresh > 0 {
					fmt.Fprintln(os.Stderr, dim(fmt.Sprintf("witnessed: %d ran, %d served fresh", testRun.Ran, testRun.Fresh)))
				}
				for key, out := range testRun.Failures {
					fmt.Fprintf(os.Stderr, "%s\n%s", red("witness failed: "+key), out)
				}
			}
			backends, err := makeBackends(chdir)
			if err != nil {
				return err
			}
			// A resolved gap is derived from coverage, which is only sound
			// when verification is clean: a dangling or stale record could
			// misreport a requirement's bucket and prune a gap that is still
			// load-bearing. Refuse rather than delete on a shaky reading.
			rep := verify.Run(spec, store, backends, testRun)
			if len(rep.Problems) > 0 {
				for _, p := range rep.Problems {
					fmt.Fprintln(os.Stderr, red(p.String()))
				}
				return fmt.Errorf("fix verification problems first")
			}
			manifest, err := corpus.LoadManifest(fsys)
			if err != nil {
				return err
			}
			pol, err := coverage.PolicyFromManifest(manifest)
			if err != nil {
				return err
			}
			cov := coverage.Evaluate(spec, rep, store, !noTest, pol)
			resolved := map[string]bool{}
			for _, g := range cov.Gaps {
				if g.State == coverage.Resolved {
					resolved[g.RequirementId] = true
				}
			}
			prunes := author.PruneResolvedGaps(store, resolved)

			if check {
				for _, up := range prunes {
					fmt.Printf("%s resolved gap lingers: %s\n", yellow("prunable:"), up.Path)
				}
				if len(prunes) > 0 {
					return fmt.Errorf("prune --check: %d resolved gaps linger", len(prunes))
				}
				fmt.Println(green("prune: clean"))
				return nil
			}
			if err := applyUpdates(chdir, prunes); err != nil {
				return err
			}
			fmt.Printf("prune: %d resolved gaps pruned\n", len(prunes))
			return nil
		},
	}
	c.Flags().BoolVar(&check, "check", false, "lint: fail when resolved gaps linger; delete nothing")
	c.Flags().BoolVar(&noTest, "no-test", false, "skip the witness run (resolved-gap pruning may under-detect)")
	return c
}
