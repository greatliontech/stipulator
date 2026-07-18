package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/policy"
)

func policyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "policy",
		Short: "Manage the accepted test policy record",
	}
	c.AddCommand(policyInitCmd())
	return c
}

// policyBackends is the registry the policy dispatch seam consults: each
// backend claims its own payload case by name.
func policyBackends() map[string]policy.Backend {
	return map[string]policy.Backend{"go": golang.Policy{}}
}

func policyInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Derive the universal-race test policy record when absent",
		Long: "Derives the accepted test policy equivalent to the universal race\n" +
			"suite witness execution currently assumes — one race-enabled ./...\n" +
			"invocation per workspace member — and writes it to " + policy.Path + "\n" +
			"only when no record exists. An existing record is the reviewed\n" +
			"contract: a matching one makes this a no-op, a diverging one is an\n" +
			"error, never a rewrite.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !treeHasGoModule(chdir) {
				return fmt.Errorf("no go.mod or go.work at the tree root; policy derivation needs a Go module or workspace")
			}
			derived, err := golang.DerivePolicy(chdir)
			if err != nil {
				return err
			}
			// The derived record must pass the same dispatch seam every
			// consumer loads through — envelope validation plus each
			// backend's payload validation — before it may be written.
			// Today's derivation cannot produce a refusable record, so this
			// guard is defense in depth against a future derivation drifting
			// from the validators; no test can currently falsify it.
			if _, err := policy.Dispatch(derived, policyBackends()); err != nil {
				return err
			}
			rendered, err := policy.Render(derived)
			if err != nil {
				return err
			}
			full := filepath.Join(chdir, filepath.FromSlash(policy.Path))
			existing, err := os.ReadFile(full)
			switch {
			case err == nil:
				if bytes.Equal(existing, rendered) {
					fmt.Printf("%s already matches the derived universal-race policy; nothing to do\n", policy.Path)
					return nil
				}
				return fmt.Errorf("%s already exists and diverges from the derived universal-race policy (%s); the committed record is the reviewed contract and is never rewritten — edit or remove it deliberately",
					policy.Path, firstDivergence(existing, rendered))
			case !errors.Is(err, fs.ErrNotExist):
				return err
			}
			// O_EXCL closes the read-then-write window: a record appearing
			// between the absent check and this write — a concurrent init or
			// a user edit — fails the create instead of being clobbered.
			if err := writeFileExclusiveAt(chdir, policy.Path, rendered); err != nil {
				return err
			}
			fmt.Println("configuration break: this record is the explicit, reviewed test policy that unified execution will honor for suite health and witness evidence; review and commit it — witness execution stops assuming a universal race invocation once it consumes the record")
			return nil
		},
	}
}

// firstDivergence names the first line where an existing record departs
// from the derivation, so a refusal states the divergence rather than a
// bare mismatch.
func firstDivergence(existing, derived []byte) string {
	el := strings.Split(string(existing), "\n")
	dl := strings.Split(string(derived), "\n")
	for i := 0; i < len(el) || i < len(dl); i++ {
		var e, d string
		if i < len(el) {
			e = el[i]
		}
		if i < len(dl) {
			d = dl[i]
		}
		if e != d {
			return fmt.Sprintf("line %d: record has %q, derivation has %q", i+1, e, d)
		}
	}
	return "records differ"
}

func treeHasGoModule(dir string) bool {
	for _, name := range []string{"go.mod", "go.work"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// writeFileExclusiveAt writes a new file, failing if it already exists.
func writeFileExclusiveAt(dir, rel string, data []byte) error {
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
