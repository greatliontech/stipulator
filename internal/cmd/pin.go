package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/records"
)

func pinCmd() *cobra.Command {
	var reqs []string
	c := &cobra.Command{
		Use:   "pin",
		Short: guidanceShort("pin"),
		Long:  guidanceHelp("pin"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(reqs) > 0 {
				// The ids form re-consents clause text only; a shape
				// mismatch on the named requirement's bindings would
				// survive it untouched, so report it rather than let
				// "pins current" read as quiescence while the gate
				// stays red.
				store, err := records.Load(os.DirFS(chdir))
				if err != nil {
					return err
				}
				backends, err := makeBackends(cmd.Context(), chdir)
				if err != nil {
					return err
				}
				wanted := map[string]bool{}
				for _, id := range reqs {
					wanted[id] = true
				}
				mismatched := records.ShapeMismatched(store, reqs, author.ResolveShapes(store, backends, wanted, func(symbol string, err error) {
					fmt.Fprintf(os.Stderr, "pin: skipping %s: %v\n", symbol, err)
				}))
				for _, id := range reqs {
					ups, err := author.Editorial(os.DirFS(chdir), id)
					if errors.Is(err, author.ErrNothingStale) {
						if syms := mismatched[id]; len(syms) > 0 {
							fmt.Printf("%s: clause pins current; shape of %s moved — the ids form re-consents clause text only, blanket stipulator pin re-pins shapes\n", id, strings.Join(syms, ", "))
						} else {
							fmt.Printf("%s: pins current\n", id)
						}
						continue
					}
					if err != nil {
						return err
					}
					if err := applyUpdates(chdir, ups); err != nil {
						return err
					}
					if syms := mismatched[id]; len(syms) > 0 {
						fmt.Printf("%s: %d file(s) re-pinned; shape of %s moved — blanket stipulator pin re-pins shapes\n", id, len(ups), strings.Join(syms, ", "))
					} else {
						fmt.Printf("%s: %d file(s) re-pinned\n", id, len(ups))
					}
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
			updates, preserved, reshaped, err := records.Pin(store, hashes, author.ResolveShapes(store, backends, nil, func(symbol string, err error) {
				fmt.Fprintf(os.Stderr, "pin: skipping %s: %v\n", symbol, err)
			}))
			if err != nil {
				return err
			}
			paths := make([]string, 0, len(updates))
			for p := range updates {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			ups := make([]author.Update, 0, len(paths))
			for _, p := range paths {
				ups = append(ups, author.Update{Path: p, Content: updates[p]})
			}
			author.StampPriors(store, ups)
			if err := applyUpdates(chdir, ups); err != nil {
				return err
			}
			// A no-op beside preserved differing pins must not read as
			// quiescence: "all pins current" is false exactly then.
			switch {
			case len(updates) == 0 && len(preserved) == 0:
				fmt.Println("all pins current")
			case len(updates) == 0:
				fmt.Println("no pins backfilled")
			}
			if len(reshaped) > 0 {
				fmt.Printf("shape pins refreshed (bound implementation moved): %s\n", strings.Join(reshaped, ", "))
			}
			if len(preserved) > 0 {
				fmt.Printf("awaiting re-consent (pin --req): %s\n", strings.Join(preserved, ", "))
			}
			return nil
		},
	}
	c.Flags().StringArrayVar(&reqs, "req", nil, "requirement identifier to editorially re-pin (repeatable)")
	registerReqCompletions(c, "req")
	return c
}

