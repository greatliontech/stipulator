package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
)

func verifyCmd() *cobra.Command {
	var noTest bool
	c := &cobra.Command{
		Use:   "verify",
		Short: guidanceShort("verify"),
		RunE: func(cmd *cobra.Command, args []string) error {
			prepared, err := mustPrepare(chdir)
			if err != nil {
				return err
			}
			spec, store := prepared.Spec, prepared.Store
			// Record hygiene decides before any witness executes: the
			// problems are the verification's answer whatever a run
			// would say (REQ-check-preparation).
			if err := refuseHygiene(prepared.Hygiene); err != nil {
				return err
			}
			pc, gb, err := servedBackend(cmd.Context(), store, !noTest)
			if err != nil {
				return err
			}
			defer gb.Close()
			var testRun *verify.TestRun
			if !noTest {
				tr, err := witnessRun(cmd.Context(), pc, gb)
				if err != nil {
					return err
				}
				testRun = tr
			}
			rep := verify.Run(spec, store, map[string]verify.Backend{"go": gb}, testRun)
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
				return exitStatus(1)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&noTest, "no-test", false, "skip running tests (no witnesses)")
	return c
}
