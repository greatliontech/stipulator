package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

func verifyCmd() *cobra.Command {
	var noTest bool
	c := &cobra.Command{
		Use:   "verify",
		Short: "Check records against the corpus and code",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(chdir)
			if err != nil {
				return err
			}
			store, err := records.Load(os.DirFS(chdir))
			if err != nil {
				return err
			}
			var testRun *verify.TestRun
			if !noTest {
				tr, err := golang.RunTests(chdir)
				if err != nil {
					return err
				}
				testRun = tr
			}
			backends, err := makeBackends(chdir)
			if err != nil {
				return err
			}
			rep := verify.Run(spec, store, backends, testRun)
			for _, p := range rep.Problems {
				fmt.Fprintln(os.Stderr, p)
			}
			for _, r := range rep.Results {
				if r.Resolution == verify.NotFound {
					fmt.Fprintf(os.Stderr, "%s: broken: symbol %s not found (binding for %s)\n", r.Path, r.Symbol, r.RequirementId)
				}
				if r.Shape == verify.ShapeMismatch {
					fmt.Fprintf(os.Stderr, "%s: broken: shape of %s moved (binding for %s)\n", r.Path, r.Symbol, r.RequirementId)
				}
				if r.TestOutcome == verify.TestFailed {
					fmt.Fprintf(os.Stderr, "%s: broken: bound test %s failed (binding for %s)\n", r.Path, r.Symbol, r.RequirementId)
				}
				if testRun != nil && r.TestOutcome == verify.TestNotRun && r.Role == stipulatorv1.BindingRole_BINDING_ROLE_TESTS {
					fmt.Fprintf(os.Stderr, "%s: broken: bound test %s produced no outcome — unwitnessed (binding for %s)\n", r.Path, r.Symbol, r.RequirementId)
				}
			}
			fmt.Printf("bindings: %d pinned, %d stale; shapes: %d pinned, %d unpinned; broken: %d symbols, %d shapes, %d failed tests, %d unwitnessed; unverified: %d; tests passed: %d; registrations: %d; gaps: %d\n",
				rep.Pinned, rep.Stale, rep.ShapePinned, rep.ShapeUnpinned,
				rep.Broken, rep.ShapeMismatch, rep.TestsFailed, rep.TestsNotRun,
				rep.Unverified, rep.TestsPassed, len(rep.Registrations), len(store.Gaps))
			// verify fails only on verification errors; red evidence is
			// bucket data for the gate, which decides gap-excusability.
			if len(rep.Problems) > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&noTest, "no-test", false, "skip running tests (no witnesses)")
	return c
}
