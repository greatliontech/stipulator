package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/gitfs"
	"github.com/greatliontech/stipulator/internal/harden"
	"github.com/greatliontech/stipulator/internal/records"
)

func hardenCmd() *cobra.Command {
	var reqs, symbols []string
	var budget, jobs int
	var timeout time.Duration
	var force, staged bool
	c := &cobra.Command{
		Use:   "harden",
		Short: "Mutation-test bound implementations against their bound witnesses",
		Long: "Break each bound symbol's body on purpose and check that a witness notices:\n" +
			"each symbol is mutated once against the union of the witness-role tests of\n" +
			"every requirement it implements. Survivors are findings, never gate failures;\n" +
			"kill-sheets are recorded under .stipulator/hardening/, valid while the body\n" +
			"hash and each bound witness's content match.",
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
			if staged {
				changed, err := gitfs.Changed(chdir)
				if err != nil {
					return err
				}
				headFS, err := gitfs.FS(chdir, "HEAD")
				if err != nil {
					return err
				}
				head := func(p string) ([]byte, bool) {
					b, err := fs.ReadFile(headFS, p)
					return b, err == nil
				}
				printStagedScope(harden.StagedScope(spec, store, gb, changed, head))
				return nil
			}
			targets := harden.Plan(spec, store, reqs, symbols)
			if len(targets) == 0 {
				return fmt.Errorf("no targets: no go implements-bindings match the selection")
			}
			rep, err := harden.Run(context.Background(), chdir, gb, store, targets, harden.Options{
				Budget: budget, Timeout: timeout, Force: force, Jobs: jobs,
			})
			if err != nil {
				return err
			}
			for path, content := range rep.Records(store) {
				if err := writeFileAt(chdir, path, content); err != nil {
					return err
				}
			}
			open, attested := 0, 0
			for _, res := range rep.Results {
				reqs := strings.Join(res.Requirements, ",")
				attestedHere := map[string]string{}
				for _, a := range res.Attested {
					attestedHere[a.Position+"|"+a.Operator] = a.Reason
				}
				attested += len(res.Attested)
				switch {
				case res.SkippedNoTest:
					fmt.Printf("skip  %s (%s): no bound witnesses\n", res.Symbol, reqs)
				case res.SkippedNotFunc:
					fmt.Printf("skip  %s (%s): not a function — nothing to mutate\n", res.Symbol, reqs)
				case res.Cached:
					fmt.Printf("cache %s: %d/%d killed, %d survivors (%d attested)\n", res.Symbol, res.Killed, res.Mutants, len(res.Survivors), len(res.Attested))
					open += len(res.Survivors) - len(res.Attested)
				default:
					fmt.Printf("run   %s (%s): %d/%d killed, %d discarded, %d witnesses\n", res.Symbol, reqs, res.Killed, res.Mutants, res.Discarded, len(res.Witnesses))
					for _, s := range res.Survivors {
						if reason, ok := attestedHere[s.Position+"|"+s.Operator]; ok {
							fmt.Printf("      ATTESTED %s: %s — %s\n", s.Position, s.Operator, reason)
							continue
						}
						fmt.Printf("      SURVIVOR %s: %s\n", s.Position, s.Operator)
						open++
					}
				}
			}
			fmt.Printf("harden: %d open survivors, %d attested\n", open, attested)
			return nil
		},
	}
	c.Flags().StringArrayVar(&reqs, "req", nil, "requirement identifiers selecting symbols to harden; each runs against its full witness union (repeatable; empty = all bound)")
	c.Flags().StringArrayVar(&symbols, "symbol", nil, "implementation symbols to harden (repeatable filter)")
	c.Flags().IntVar(&budget, "budget", 24, "mutant budget per symbol (0 = all)")
	c.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "test timeout per mutant invocation (a mixed rapid/plain witness union runs up to two)")
	c.Flags().BoolVar(&force, "force", false, "rerun targets whose kill-sheet pins (body hash, witness content, operator set, toolchain) still match")
	c.Flags().IntVar(&jobs, "jobs", 0, "concurrent mutant runs (0 = half the CPUs; load-induced flakes read as kills, so the default hedges)")
	c.Flags().BoolVar(&staged, "staged-diff", false, "classify the working-tree delta vs HEAD instead of mutating: which changed surfaces harden covers and which need manual mutation")
	registerReqCompletions(c, "req")
	return c
}

// printStagedScope renders the staged-delta classification: the coverable
// surfaces first (the operator hardens these), then the manual tail grouped
// by why harden cannot reach it, then a one-line roll-up. It is a report,
// never a gate (REQ-harden-staged-scope).
func printStagedScope(rep *harden.StagedReport) {
	if len(rep.Entries) == 0 {
		fmt.Println("staged: no changed files vs HEAD")
		return
	}
	fmt.Println("staged-delta hardening surface (working tree vs HEAD):")
	var coverable, manual, skipped int
	for _, e := range rep.Entries {
		line := fmt.Sprintf("  %-32s%s", e.Class, e.Path)
		if e.Symbol != "" {
			line += "  " + e.Symbol
		}
		if len(e.Requirements) > 0 {
			line += " (" + strings.Join(e.Requirements, ",") + ")"
		}
		fmt.Println(line)
		switch e.Class {
		case harden.Covered:
			coverable++
		case harden.GeneratedOrData:
			skipped++
		default:
			manual++
		}
	}
	fmt.Printf("staged: %d coverable (harden them), %d need manual mutation, %d generated/data skipped\n",
		coverable, manual, skipped)
}
