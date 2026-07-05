package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/harden"
	"github.com/greatliontech/stipulator/internal/records"
)

func hardenCmd() *cobra.Command {
	var reqs, symbols []string
	var budget int
	var timeout time.Duration
	var force bool
	c := &cobra.Command{
		Use:   "harden",
		Short: "Mutation-test bound implementations against their bound tests",
		Long: "Break each target's body on purpose and check that a bound test notices.\n" +
			"Survivors are findings, never gate failures; kill-sheets are recorded under\n" +
			".stipulator/hardening/, valid while the body hash matches.",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(chdir)
			if err != nil {
				return err
			}
			store, err2 := records.Load(os.DirFS(chdir))
			if err2 != nil {
				return err2
			}
			gb, err := golang.New(chdir)
			if err != nil {
				return err
			}
			targets := harden.Plan(spec, store, reqs, symbols)
			if len(targets) == 0 {
				return fmt.Errorf("no targets: no go implements-bindings match the selection")
			}
			rep, err := harden.Run(context.Background(), chdir, gb, store, targets, harden.Options{
				Budget: budget, Timeout: timeout, Force: force,
			})
			if err != nil {
				return err
			}
			for path, content := range rep.Records(store) {
				if err := writeFileAt(chdir, path, content); err != nil {
					return err
				}
			}
			survivors := 0
			for _, res := range rep.Results {
				switch {
				case res.SkippedNoTest:
					fmt.Printf("skip  %s %s: no bound tests\n", res.Requirement, res.Symbol)
				case res.Cached:
					fmt.Printf("cache %s %s: %d/%d killed, %d survivors\n", res.Requirement, res.Symbol, res.Killed, res.Mutants, len(res.Survivors))
					survivors += len(res.Survivors)
				default:
					fmt.Printf("run   %s %s: %d/%d killed, %d discarded\n", res.Requirement, res.Symbol, res.Killed, res.Mutants, res.Discarded)
					for _, s := range res.Survivors {
						fmt.Printf("      SURVIVOR %s: %s\n", s.Position, s.Operator)
						survivors++
					}
				}
			}
			fmt.Printf("harden: %d survivors\n", survivors)
			return nil
		},
	}
	c.Flags().StringArrayVar(&reqs, "req", nil, "requirement identifiers to harden (repeatable; empty = all bound)")
	c.Flags().StringArrayVar(&symbols, "symbol", nil, "implementation symbols to harden (repeatable filter)")
	c.Flags().IntVar(&budget, "budget", 24, "mutant budget per symbol (0 = all)")
	c.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "per-mutant test timeout")
	c.Flags().BoolVar(&force, "force", false, "rerun targets whose kill-sheet body hash still matches")
	registerReqCompletions(c, "req")
	return c
}
