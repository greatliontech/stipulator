package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/corpus"
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
			fmt.Fprintln(os.Stderr, dim("witnessing: go test -json -race ./..."))
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
				fmt.Fprintln(os.Stderr, red(p.String()))
			}
			if len(rep.Problems) > 0 {
				os.Exit(1)
			}
			manifest, err := corpus.LoadManifest(os.DirFS(chdir))
			if err != nil {
				return err
			}
			pol, err := coverage.PolicyFromManifest(manifest)
			if err != nil {
				return err
			}
			cov := coverage.Evaluate(spec, rep, store, true, pol)
			printCoverage(cov)
			if !cov.GatePasses() {
				for _, v := range cov.Violations {
					fmt.Fprintf(os.Stderr, "%s %s is red and no gap names it\n", red("violation:"), bold(v))
				}
				fmt.Println(red("gate: fail"))
				os.Exit(1)
			}
			fmt.Println(green("gate: pass"))
			return nil
		},
	}
}

// printCoverage renders the human coverage view: red requirements with
// their reasons and gap state merged inline, then one summary line.
func printCoverage(cov *coverage.Report) {
	gapByReq := map[string]coverage.Gap{}
	for _, g := range cov.Gaps {
		gapByReq[g.RequirementId] = g
	}
	for _, o := range cov.PolicyOverrides {
		fmt.Println(dim(o))
	}
	counts := map[coverage.Bucket]int{}
	width := 0
	var reds []coverage.Requirement
	for _, r := range cov.Requirements {
		counts[r.Bucket]++
		if r.Bucket == coverage.Covered || r.Bucket == coverage.Exempt {
			continue
		}
		reds = append(reds, r)
		if len(r.Id) > width {
			width = len(r.Id)
		}
	}
	for _, r := range reds {
		bucket := yellow(r.Bucket.String())
		if r.Bucket == coverage.Broken {
			bucket = red(r.Bucket.String())
		}
		gapNote := red("no gap")
		if g, ok := gapByReq[r.Id]; ok {
			gapNote = dim("gap " + g.State.String())
		}
		reason := ""
		if len(r.Reasons) > 0 {
			reason = dim(r.Reasons[0])
			if len(r.Reasons) > 1 {
				reason += dim(fmt.Sprintf(" (+%d more)", len(r.Reasons)-1))
			}
		}
		fmt.Printf("  %-9s %-*s  %s  %s\n", bucket, width, r.Id, gapNote, reason)
	}
	fmt.Printf("coverage: %s covered, %s uncovered, %s stale, %s broken, %d exempt; gaps: %d\n",
		green(fmt.Sprint(counts[coverage.Covered])), num(counts[coverage.Uncovered], yellow),
		num(counts[coverage.Stale], yellow), num(counts[coverage.Broken], red),
		counts[coverage.Exempt], len(cov.Gaps))
}
