package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

func pruneCmd() *cobra.Command {
	var check, noTest, dangling bool
	c := &cobra.Command{
		Use:   "prune",
		Short: "Delete resolved gap records",
		Long: "Deletes resolved gap records — requirement covered, any manual landing\n" +
			"condition explicitly fired: a resolved gap is satisfied, dead record weight.\n" +
			"--dangling instead deletes gap records naming requirements no longer in\n" +
			"the corpus — the explicit bulk repair, judged from corpus and records\n" +
			"alone, never part of resolved-record pruning. --check lints either mode:\n" +
			"non-zero exit when records linger, deleting nothing.",
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
			// Danglingness is a corpus-and-records fact: no witnesses, no
			// symbol resolution, and no verification gate — a dangling gap
			// IS a verification problem, so gating its repair on clean
			// verification would deadlock the repair.
			if dangling {
				present := map[string]bool{}
				for _, r := range spec.GetRequirements() {
					present[r.GetId()] = true
				}
				prunes := author.PruneDanglingGaps(store, present)
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
			var testRun *verify.TestRun
			if !noTest {
				if testRun, err = witnessRun(cmd.Context()); err != nil {
					return err
				}
				// The resolved-record evaluation is pinned to the serving
				// class (REQ-gap-resolved-pruned); the producer's mark
				// makes a wrong witness source a loud refusal.
				if err := verify.ServingClassRequired(testRun); err != nil {
					return err
				}
			}
			backends, err := makeBackends(cmd.Context(), chdir)
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
	c.Flags().BoolVar(&check, "check", false, "lint: fail when records linger; delete nothing")
	c.Flags().BoolVar(&noTest, "no-test", false, "skip the witness run (resolved-gap pruning may under-detect)")
	c.Flags().BoolVar(&dangling, "dangling", false, "delete gap records naming requirements no longer in the corpus")
	return c
}
