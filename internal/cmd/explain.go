package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/backends/golang"
)

func explainCmd() *cobra.Command {
	var reason, pkgPath, symbol string
	c := &cobra.Command{
		Use:   "explain",
		Short: guidanceShort("explain"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pkgPath, symbol, err := golang.ResolveCulprit(reason, pkgPath, symbol, func(name string) string { return "--" + name })
			if err != nil {
				return err
			}
			chain, view, err := explainChain(cmd.Context(), chdir, pkgPath, symbol)
			if err != nil {
				return withRecordPath(err)
			}
			if chain.Arm == "" {
				fmt.Println("explain: no chain — " + pkgPath + "." + symbol + " is not a culprit in the policy views")
				return nil
			}
			fmt.Printf("%s %s.%s — %s (view: %s)\n", bold("explain:"), pkgPath, symbol, chain.Arm, view)
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
	c.Flags().StringVar(&reason, "reason", "", "a witness's uncacheable reason to parse the culprit from")
	c.Flags().StringVar(&pkgPath, "package", "", "culprit package path (with --symbol, overrides --reason)")
	c.Flags().StringVar(&symbol, "symbol", "", "culprit variable name")
	return c
}

// explainChain is the one derivation the CLI explain calls, held in a
// variable so a rendering test can hand it a chain without a policy.
var explainChain = golang.Explain
