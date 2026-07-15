package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

func pinCmd() *cobra.Command {
	var reqs []string
	c := &cobra.Command{
		Use:   "pin",
		Short: "Backfill binding content and shape pins; --req re-consents named requirements",
		Long: "Without flags, backfills unset content pins and refreshes shape pins" +
			" - a differing content pin is never rewritten by the blanket form, so" +
			" staleness cannot be laundered. Naming requirements with --req is the" +
			" editorial re-consent: their bindings re-pin to the current clause text.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(reqs) > 0 {
				for _, id := range reqs {
					ups, err := author.Editorial(os.DirFS(chdir), id)
					if errors.Is(err, author.ErrNothingStale) {
						fmt.Printf("%s: pins current\n", id)
						continue
					}
					if err != nil {
						return err
					}
					if err := applyUpdates(chdir, ups); err != nil {
						return err
					}
					fmt.Printf("%s: %d file(s) re-pinned\n", id, len(ups))
				}
				return nil
			}
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
			backends, err := makeBackends(cmd.Context(), chdir)
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
	c.Flags().StringArrayVar(&reqs, "req", nil, "requirement identifier to editorially re-pin (repeatable)")
	registerReqCompletions(c, "req")
	return c
}
