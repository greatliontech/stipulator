package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/remedy"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/verifyrun"
	"github.com/greatliontech/stipulator/internal/views"
	"github.com/greatliontech/stipulator/internal/wire"
)

func verifyCmd() *cobra.Command {
	var noTest, jsonOut bool
	var view, filter, pathPrefix string
	var reqs []string
	c := &cobra.Command{
		Use:   remedy.VerbVerify,
		Short: guidanceShort("verify"),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := views.Scope{Ids: reqs, Filter: filter, Path: pathPrefix}
			if err := validateScoped(scope, views.ValidateVerifyView, view); err != nil {
				return err
			}
			prepared, rep, testRun, err := verifyrun.Run(cmd.Context(), cliDeps(), noTest, scope.Ids)
			if err != nil {
				return withRecordPath(err)
			}
			if err := refuseHygiene(prepared.Hygiene); err != nil {
				return err
			}
			spec, store := prepared.Spec, prepared.Store
			for _, p := range rep.Problems {
				fmt.Fprintln(os.Stderr, red(p.String()))
			}
			// The views are the projections the MCP surface serves —
			// one projection, two renderings — so "what claims this
			// symbol" is the same query at a shell as in an agent's
			// call (REQ-mcp-surfaces).
			if jsonOut {
				m, verr := views.VerifyView(rep, views.FactsFrom(spec, rep), view, scope)
				if verr != nil {
					return verr
				}
				out, verr := wire.CanonicalJSON(m)
				if verr != nil {
					return verr
				}
				if _, err := os.Stdout.Write(out); err != nil {
					return err
				}
				if len(rep.Problems) > 0 {
					return exitStatus(1)
				}
				return nil
			}
			// A scope narrows the whole report on every view
			// (REQ-mcp-views): the summary's counts and broken lines
			// are the scope's, re-tallied over the kept rows.
			sliced, verr := views.VerifyBindings(rep, views.FactsFrom(spec, rep), scope)
			if verr != nil {
				return verr
			}
			if view == "bindings" {
				printBindingRows(sliced)
				if len(rep.Problems) > 0 {
					return exitStatus(1)
				}
				return nil
			}
			rep = sliced
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
			if rep.Rehash > 0 {
				// Bindings and attestations alike: the count is of
				// consent records, its own line beside the faces.
				fmt.Printf("rehash:    %d record(s) current by source pin alone — %s; blanket %s rewrites them\n", rep.Rehash, records.RehashNote, remedy.Pin())
			}
			fmt.Printf("shapes:    %d pinned, %s unpinned, %s moved\n",
				rep.ShapePinned, num(rep.ShapeUnpinned, yellow), num(rep.ShapeMismatch, red))
			fmt.Printf("witnesses: %d passed, %s failed, %s unwitnessed\n",
				rep.TestsPassed, num(rep.TestsFailed, red), num(rep.TestsNotRun, red))
			if rep.Broken > 0 || rep.Unverified > 0 {
				fmt.Printf("symbols:   %s unresolved, %d unverified (no backend answer in this run)\n",
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
	c.Flags().BoolVar(&noTest, "no-test", false, "")
	c.Flags().StringVar(&view, "view", "", "")
	c.Flags().StringArrayVar(&reqs, "req", nil, "")
	c.Flags().StringVar(&filter, "filter", "", "")
	c.Flags().StringVar(&pathPrefix, "path", "", "")
	c.Flags().BoolVar(&jsonOut, "json", false, "")
	registerReqCompletions(c, "req")
	return c
}

// printBindingRows renders the bindings view for the operator: one line
// per claim, the facts a deletion or a rebinding decides on. The outcome
// column appears only on a witnessed report — an unwitnessed row has no
// outcome, and "not run" would misreport the records-only judgment.
func printBindingRows(rep *verify.Report) {
	rows := rep.Results
	if len(rows) == 0 {
		fmt.Println("no binding rows in scope")
		return
	}
	idWidth, symWidth := 0, 0
	for _, r := range rows {
		idWidth = max(idWidth, len(r.RequirementId))
		symWidth = max(symWidth, len(r.Symbol+clauseColumn(r)))
	}
	for _, r := range rows {
		role := strings.ToLower(strings.TrimPrefix(r.Role.String(), "BINDING_ROLE_"))
		consent := green("current")
		switch {
		case !r.ContentPinned:
			consent = yellow("stale")
		case r.Rehash:
			consent = yellow("rehash-pending")
		}
		state := "unverified"
		switch r.Resolution {
		case verify.NotFound:
			state = red("not found")
		case verify.GeneratedFile:
			state = red("generated file")
		case verify.Resolved:
			switch r.Shape {
			case verify.ShapeMismatch:
				state = red("shape moved")
			case verify.ShapeUnpinned:
				state = yellow("shape unpinned")
			default:
				state = "resolved"
			}
		}
		outcome := ""
		if rep.Witnessed && (r.Role == stipulatorv1.BindingRole_BINDING_ROLE_TESTS || r.Role == stipulatorv1.BindingRole_BINDING_ROLE_PROVES) {
			switch r.TestOutcome {
			case verify.TestPassed:
				outcome = "  " + green("passed")
			case verify.TestFailed:
				outcome = "  " + red("failed")
			case verify.TestSkipped:
				outcome = "  skipped"
			default:
				outcome = "  " + red("unwitnessed")
			}
		}
		fmt.Printf("%-*s  %-10s %-*s  %-14s  %s%s  %s\n", idWidth, r.RequirementId, role, symWidth, r.Symbol+clauseColumn(r), consent, state, outcome, dim(r.Path))
	}
	fmt.Printf("%d binding row(s)\n", len(rows))
}

// clauseColumn renders a row's clause beside its symbol; empty for a
// whole-requirement claim.
func clauseColumn(r verify.BindingResult) string {
	if r.Clause == nil {
		return ""
	}
	out := fmt.Sprintf(" clause %d", r.Clause.GetOrdinal())
	if r.Clause.GetLabel() != "" {
		out += " `" + r.Clause.GetLabel() + "`"
	}
	return out
}
