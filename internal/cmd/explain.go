package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/gofresh"

	"github.com/greatliontech/stipulator/internal/backends/golang"
)

func explainCmd() *cobra.Command {
	var reason, pkgPath, symbol, witness string
	c := &cobra.Command{
		Use:   "explain",
		Short: guidanceShort("explain"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := golang.ResolveExplain(reason, pkgPath, symbol, witness, func(name string) string { return "--" + name })
			if err != nil {
				return err
			}
			if req.Attribution != "" {
				fmt.Println("explain: no chain — the reason is its own attribution: " + req.Attribution)
				return nil
			}
			var chain gofresh.Chain
			var view, subject string
			if req.Witness != "" {
				subject = req.Witness
				chain, view, err = explainWitnessChain(cmd.Context(), chdir, req.Witness)
			} else {
				subject = req.Package + "." + req.Symbol
				chain, view, err = explainChain(cmd.Context(), chdir, req.Package, req.Symbol)
			}
			if err != nil {
				return withRecordPath(err)
			}
			if chain.Arm == "" {
				fmt.Println("explain: no chain — " + subject + " is not a culprit in the policy views")
				return nil
			}
			fmt.Printf("%s %s — %s (view: %s)\n", bold("explain:"), subject, chain.Arm, view)
			for i, l := range chain.Links {
				line := fmt.Sprintf("  %2d  %-8s %s.%s", i+1, l.Kind, l.Package, l.Symbol)
				if l.Callee != "" {
					line += "  → " + l.Callee
				}
				if l.Clause != "" {
					line += "  [" + l.Clause + "]"
				}
				if l.Pos != "" {
					line += "  " + dim(l.Pos)
				}
				fmt.Println(line)
			}
			if chain.Omitted > 0 {
				fmt.Fprintf(os.Stderr, "%d link(s) omitted by the chain bound\n", chain.Omitted)
			}
			return nil
		},
	}
	c.Flags().StringVar(&reason, "reason", "", "")
	c.Flags().StringVar(&pkgPath, "package", "", "")
	c.Flags().StringVar(&symbol, "symbol", "", "")
	c.Flags().StringVar(&witness, "witness", "", "")
	return c
}

// explainChain and explainWitnessChain are the two derivations the CLI
// explain calls, held in variables so a rendering test can hand them a
// chain without a policy.
var (
	explainChain        = golang.Explain
	explainWitnessChain = golang.ExplainWitness
)
