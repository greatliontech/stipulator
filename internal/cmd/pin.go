package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

func pinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pin",
		Short: "Backfill binding content and shape pins",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(chdir)
			if err != nil {
				return err
			}
			store, err := records.Load(os.DirFS(chdir))
			if err != nil {
				return err
			}
			hashes := map[string]string{}
			for _, r := range spec.GetRequirements() {
				hashes[r.GetId()] = r.GetContentHash()
			}
			backends, err := makeBackends(chdir)
			if err != nil {
				return err
			}
			shapes := map[string]string{}
			for _, bf := range store.Bindings {
				for _, b := range bf.Set.GetBindings() {
					be, ok := backends[b.GetBackend()]
					if !ok {
						continue
					}
					res, shape, err := be.Resolve(b.GetSymbol())
					switch {
					case err != nil:
						fmt.Fprintf(os.Stderr, "pin: skipping %s: %v\n", b.GetSymbol(), err)
					case res == verify.Resolved:
						shapes[records.ShapeKey(b.GetBackend(), b.GetSymbol())] = shape
					}
				}
			}
			updates, err := records.Pin(store, hashes, shapes)
			if err != nil {
				return err
			}
			paths := make([]string, 0, len(updates))
			for p := range updates {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			for _, p := range paths {
				if err := os.WriteFile(filepath.Join(chdir, filepath.FromSlash(p)), updates[p], 0o644); err != nil {
					return err
				}
				fmt.Println("pinned", p)
			}
			if len(updates) == 0 {
				fmt.Println("all pins current")
			}
			return nil
		},
	}
}
