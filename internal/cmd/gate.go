package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

func gateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gate",
		Short: "Coverage buckets and the gate verdict",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(chdir)
			if err != nil {
				return err
			}
			store, err := records.Load(os.DirFS(chdir))
			if err != nil {
				return err
			}
			testRun, err := golang.RunTests(chdir)
			if err != nil {
				return err
			}
			backends, err := makeBackends(chdir)
			if err != nil {
				return err
			}
			rep := verify.Run(spec, store, backends, testRun)
			for _, p := range rep.Problems {
				fmt.Fprintln(os.Stderr, p)
			}
			if len(rep.Problems) > 0 {
				os.Exit(1)
			}
			cov := coverage.Evaluate(spec, rep, store, true)
			counts := map[coverage.Bucket]int{}
			for _, r := range cov.Requirements {
				counts[r.Bucket]++
				if r.Bucket != coverage.Covered && r.Bucket != coverage.Exempt {
					fmt.Printf("%s: %s (%s)\n", r.Bucket, r.Id, strings.Join(r.Reasons, "; "))
				}
			}
			for _, g := range cov.Gaps {
				fmt.Printf("gap %s: %s (%s)\n", g.State, g.RequirementId, g.Path)
			}
			fmt.Printf("coverage: %d covered, %d uncovered, %d stale, %d broken, %d exempt; gaps: %d\n",
				counts[coverage.Covered], counts[coverage.Uncovered], counts[coverage.Stale],
				counts[coverage.Broken], counts[coverage.Exempt], len(cov.Gaps))
			if !cov.GatePasses() {
				for _, v := range cov.Violations {
					fmt.Fprintf(os.Stderr, "gate: %s is red and no gap names it\n", v)
				}
				os.Exit(1)
			}
			fmt.Println("gate: pass")
			return nil
		},
	}
}
