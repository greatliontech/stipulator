package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/index"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

func fmtCmd() *cobra.Command {
	var check, force, noTest bool
	c := &cobra.Command{
		Use:   "fmt",
		Short: "Regenerate folder indexes and prune resolved gaps",
		Long: "Writes the generated README.md index for every corpus directory and\n" +
			"deletes gap records whose requirements are covered. --check lints instead:\n" +
			"non-zero exit when anything would change, writing nothing.",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(chdir)
			if err != nil {
				return err
			}
			fsys := os.DirFS(chdir)
			stale, err := index.Stale(fsys, spec, force)
			if err != nil {
				return err
			}

			store, err := records.Load(fsys)
			if err != nil {
				return err
			}
			var testRun *verify.TestRun
			if !noTest {
				fmt.Fprintln(os.Stderr, dim("witnessing: go test -json -race ./..."))
				if testRun, err = golang.RunTests(chdir); err != nil {
					return err
				}
			}
			backends, err := makeBackends(chdir)
			if err != nil {
				return err
			}
			rep := verify.Run(spec, store, backends, testRun)
			if len(rep.Problems) > 0 {
				for _, p := range rep.Problems {
					fmt.Fprintln(os.Stderr, red(p.String()))
				}
				return fmt.Errorf("fix verification problems first")
			}
			manifest, err := corpus.LoadManifest(os.DirFS(chdir))
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
				for _, p := range stale {
					fmt.Printf("%s index %s\n", yellow("stale:"), p)
				}
				for _, up := range prunes {
					fmt.Printf("%s resolved gap lingers: %s\n", yellow("stale:"), up.Path)
				}
				if len(stale) > 0 || len(prunes) > 0 {
					return fmt.Errorf("fmt --check: %d stale", len(stale)+len(prunes))
				}
				fmt.Println(green("fmt: clean"))
				return nil
			}

			indexes := index.Build(spec)
			for _, p := range stale {
				if err := writeFileAt(chdir, p, indexes[p]); err != nil {
					return err
				}
			}
			if err := applyUpdates(chdir, prunes); err != nil {
				return err
			}
			fmt.Printf("fmt: %d indexes written, %d resolved gaps pruned\n", len(stale), len(prunes))
			return nil
		},
	}
	c.Flags().BoolVar(&check, "check", false, "lint: fail when indexes are stale or resolved gaps linger; write nothing")
	c.Flags().BoolVar(&force, "force", false, "replace README.md files that are not generated indexes")
	c.Flags().BoolVar(&noTest, "no-test", false, "skip the witness run (resolved-gap pruning may under-detect)")
	return c
}
