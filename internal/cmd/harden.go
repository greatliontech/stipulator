package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
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
		Short: "Mutation-test bound implementations against their bound witnesses",
		Long: "Break each bound symbol's body on purpose and check that a witness notices:\n" +
			"each symbol is mutated once against the union of the witness-role tests of\n" +
			"every requirement it implements. Survivors are findings, never gate failures;\n" +
			"kill-sheets are recorded under .stipulator/hardening/, valid while the body\n" +
			"hash and witness set match.",
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
				reqs := strings.Join(res.Requirements, ",")
				switch {
				case res.SkippedNoTest:
					fmt.Printf("skip  %s (%s): no bound witnesses\n", res.Symbol, reqs)
				case res.SkippedNotFunc:
					fmt.Printf("skip  %s (%s): not a function — nothing to mutate\n", res.Symbol, reqs)
				case res.Cached:
					fmt.Printf("cache %s: %d/%d killed, %d survivors\n", res.Symbol, res.Killed, res.Mutants, len(res.Survivors))
					survivors += len(res.Survivors)
				default:
					fmt.Printf("run   %s (%s): %d/%d killed, %d discarded, %d witnesses\n", res.Symbol, reqs, res.Killed, res.Mutants, res.Discarded, len(res.Witnesses))
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
	c.Flags().StringArrayVar(&reqs, "req", nil, "requirement identifiers selecting symbols to harden; each runs against its full witness union (repeatable; empty = all bound)")
	c.Flags().StringArrayVar(&symbols, "symbol", nil, "implementation symbols to harden (repeatable filter)")
	c.Flags().IntVar(&budget, "budget", 24, "mutant budget per symbol (0 = all)")
	c.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "test timeout per mutant invocation (a mixed rapid/plain witness union runs up to two)")
	c.Flags().BoolVar(&force, "force", false, "rerun targets whose kill-sheet pins (body hash, witness set) still match")
	registerReqCompletions(c, "req")
	return c
}
