package cmd

import (
	"fmt"
	"os"
	"strings"

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
				fmt.Fprintln(os.Stderr, dim("witnessing: fresh-checked; stale and unproven tests run (-race)"))
				tr, err := golang.RunTestsFresh(chdir)
				if err != nil {
					return err
				}
				if tr.Ran+tr.Fresh > 0 {
					fmt.Fprintln(os.Stderr, dim(fmt.Sprintf("witnessed: %d ran, %d served fresh, %d uncacheable", tr.Ran, tr.Fresh, tr.Uncached)))
				}
				for key, out := range tr.Failures {
					fmt.Fprintf(os.Stderr, "%s\n%s", red("witness failed: "+key), out)
				}
				testRun = tr
			}
			backends, err := makeBackends(chdir)
			if err != nil {
				return err
			}
			rep := verify.Run(spec, store, backends, testRun)
			for _, p := range rep.Problems {
				fmt.Fprintln(os.Stderr, red(p.String()))
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
			fmt.Printf("claims:    %d bindings (%s stale), %d gaps, %d registrations\n",
				rep.Pinned+rep.Stale, num(rep.Stale, yellow), len(store.Gaps), len(rep.Registrations))
			fmt.Printf("shapes:    %d pinned, %s unpinned, %s moved\n",
				rep.ShapePinned, num(rep.ShapeUnpinned, yellow), num(rep.ShapeMismatch, red))
			fmt.Printf("witnesses: %d passed, %s failed, %s unwitnessed\n",
				rep.TestsPassed, num(rep.TestsFailed, red), num(rep.TestsNotRun, red))
			if rep.Broken > 0 || rep.Unverified > 0 {
				fmt.Printf("symbols:   %s unresolved, %d unverified (no backend in this run)\n",
					num(rep.Broken, red), rep.Unverified)
			}
			for _, sig := range rep.Signatures {
				label := "rearchitecture"
				if sig.Label == verify.SemanticDrift {
					label = "semantic drift"
				}
				fmt.Printf("signature: %s %s (%s)\n", label, sig.RequirementId, strings.Join(sig.Evidence, "; "))
			}
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
