package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/harden"
	"github.com/greatliontech/stipulator/internal/records"
)

func targetsCmd() *cobra.Command {
	var reqs, symbols []string
	var out string
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
	registerReqCompletions(c, "req")
	return c
}
