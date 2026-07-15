package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	surfacewire "github.com/greatliontech/stipulator/bindingsurface"
	"github.com/greatliontech/stipulator/internal/bindingsurface"
	"github.com/greatliontech/stipulator/internal/records"
)

func targetsCmd() *cobra.Command {
	var reqs, backends, symbols []string
	var out string
	c := &cobra.Command{
		Use:   "targets",
		Short: "Export backend-independent binding surfaces",
		Long: "Emit Stipulator's versioned binding-surface ProtoJSON report. Exact requirement,\n" +
			"implementation backend, and implementation symbol filters select complete surfaces\n" +
			"without changing their relationship identifiers.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(chdir)
			if err != nil {
				return err
			}
			store, err := records.Load(os.DirFS(chdir))
			if err != nil {
				return err
			}
			report, err := bindingsurface.Derive(spec, store)
			if err != nil {
				return err
			}
			report, err = bindingsurface.Filter(report, reqs, backends, symbols)
			if err != nil {
				return err
			}
			doc, err := surfacewire.MarshalJSON(report)
			if err != nil {
				return err
			}
			doc = append(doc, '\n')
			if out == "" {
				_, err := cmd.OutOrStdout().Write(doc)
				return err
			}
			if err := writeAtomicFile(out, doc); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d surfaces)\n", out, len(report.GetSurfaces()))
			return nil
		},
	}
	c.Flags().StringArrayVar(&reqs, "req", nil, "implementing requirement identifiers filter (repeatable)")
	c.Flags().StringArrayVar(&backends, "backend", nil, "implementation backends filter (repeatable)")
	c.Flags().StringArrayVar(&symbols, "symbol", nil, "implementation symbols filter (repeatable)")
	c.Flags().StringVar(&out, "out", "", "write the export here instead of stdout")
	registerReqCompletions(c, "req")
	return c
}

func writeAtomicFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".stipulator-targets-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	remove := true
	defer func() {
		if remove {
			os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := atomicReplace(name, path); err != nil {
		return err
	}
	remove = false
	return nil
}
