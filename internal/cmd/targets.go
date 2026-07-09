package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/gitfs"
	"github.com/greatliontech/stipulator/internal/harden"
	"github.com/greatliontech/stipulator/internal/records"
)

func targetsCmd() *cobra.Command {
	var reqs, symbols []string
	var out string
	var staged bool
	c := &cobra.Command{
		Use:   "targets",
		Short: "Export the mutation targets: bound implementations with their witness unions",
		Long: "Emit stipulator's targets export — every go implements-binding paired with\n" +
			"the union of the witness-role tests of the requirements it implements, and\n" +
			"those requirement identifiers as labels. A mutation engine (gomutant) consumes\n" +
			"the export and writes its findings document; stipulator reads the findings\n" +
			"back by label. The format is stipulator's contract: stable, versioned JSON.",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(chdir)
			if err != nil {
				return err
			}
			store, err := records.Load(os.DirFS(chdir))
			if err != nil {
				return err
			}
			if staged {
				gb, err := golang.New(chdir)
				if err != nil {
					return err
				}
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
			doc, err := harden.ExportTargets(targets)
			if err != nil {
				return err
			}
			doc = append(doc, '\n')
			if out == "" {
				_, err := cmd.OutOrStdout().Write(doc)
				return err
			}
			if err := os.WriteFile(out, doc, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d targets)\n", out, len(targets))
			return nil
		},
	}
	c.Flags().StringArrayVar(&reqs, "req", nil, "requirement identifiers selecting symbols (repeatable; empty = all bound)")
	c.Flags().StringArrayVar(&symbols, "symbol", nil, "implementation symbols filter (repeatable)")
	c.Flags().StringVar(&out, "out", "", "write the export here instead of stdout")
	c.Flags().BoolVar(&staged, "staged-diff", false, "classify the working-tree delta vs HEAD instead of exporting: which changed surfaces the mutation flow covers and which need manual mutation")
	c.MarkFlagsMutuallyExclusive("staged-diff", "out")
	c.MarkFlagsMutuallyExclusive("staged-diff", "req")
	c.MarkFlagsMutuallyExclusive("staged-diff", "symbol")
	registerReqCompletions(c, "req")
	return c
}

// printStagedScope renders the staged-delta classification: the coverable
// surfaces first, then the manual tail grouped by why the mutation flow
// cannot reach it, then a one-line roll-up. It is a report, never a gate
// (REQ-harden-staged-scope).
func printStagedScope(rep *harden.StagedReport) {
	if len(rep.Entries) == 0 {
		fmt.Println("staged: no changed files vs HEAD")
		return
	}
	fmt.Println("staged-delta mutation surface (working tree vs HEAD):")
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
	fmt.Printf("staged: %d coverable, %d need manual mutation, %d generated/data skipped\n",
		coverable, manual, skipped)
}
