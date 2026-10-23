package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/remedy"
	"github.com/greatliontech/stipulator/internal/verbcore"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/views"
	"github.com/greatliontech/stipulator/internal/wire"
)

func checkCmd() *cobra.Command {
	var jsonOut, quiet, full bool
	var ids []string
	c := &cobra.Command{
		Use:   "check",
		Short: guidanceShort("check"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && quiet {
				return fmt.Errorf("give either --json or --quiet")
			}
			if !jsonOut && !quiet {
				errs := errStyle()
				if full {
					fmt.Fprintln(os.Stderr, errs.dim("checking: one execution of the accepted test policy"))
				} else {
					fmt.Fprintln(os.Stderr, errs.dim("checking: serving fresh witnesses, executing the stale remainder"))
				}
			}
			scopeIDs, err := verbcore.SplitIDLists(ids)
			if err != nil {
				return err
			}
			res, err := check.Run(cmd.Context(), chdir, full, scopeIDs)
			if err != nil {
				if !quiet {
					renderNotices(os.Stderr, verify.NoticesOf(err))
				}
				return err
			}
			switch {
			case jsonOut:
				out, err := wire.CanonicalJSON(res)
				if err != nil {
					return err
				}
				if _, err := os.Stdout.Write(out); err != nil {
					return err
				}
			case quiet:
				// Exit code only, for CI.
			default:
				renderCheck(os.Stdout, os.Stderr, res)
			}
			if !res.GetPassed() {
				return exitStatus(1)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "")
	c.Flags().BoolVar(&full, "full", false, "")
	c.Flags().StringArrayVar(&ids, "ids", nil, "")
	return c
}

// renderCheck prints the human view of one check result. Every line is a
// projection of the message — the wire result is the one source, so the
// human and machine surfaces cannot drift.
func renderCheck(stdout, stderr io.Writer, res *stipulatorv1.CheckResult) {
	out, errs := styleFor(stdout), styleFor(stderr)
	renderWitnessCacheLoad(stderr, res.GetWitnessCacheLoad())
	for _, p := range res.GetCompileProblems() {
		fmt.Fprintln(stderr, errs.red(p.GetPath()+": "+p.GetMessage()))
	}
	if p := res.GetPolicyProblem(); p != nil {
		fmt.Fprintln(stderr, errs.red(p.GetPath()+": "+p.GetMessage()))
	}
	renderNotices(stderr, res.GetPolicyNotices())
	renderNotices(stderr, res.GetResolutionNotices())
	if ex := res.GetExecution(); ex != nil {
		fmt.Fprintln(stderr, errs.dim(fmt.Sprintf("witnessed: %d executed, %d uncacheable",
			res.GetTestsExecuted(), res.GetTestsUncacheable())))
		renderInvocationWitnesses(stderr, res.GetInvocationWitnesses())
		renderUncacheableHistogram(stderr, res.GetUncacheableReasons())
		if d := res.GetWitnessPublicationDegraded(); d != "" {
			fmt.Fprintln(stderr, errs.dim("freshness publication degraded: "+d))
		}
		for _, d := range ex.GetDiagnostics() {
			fmt.Fprintf(stderr, "%s\n%s", errs.red(views.DiagnosticHeading(d)), d.GetOutput())
			if d.GetTruncated() {
				fmt.Fprintln(stderr, errs.dim("(output truncated)"))
			}
		}
	} else if !res.GetSuiteHealthJudged() && res.GetPolicyProblem() == nil && len(res.GetCompileProblems()) == 0 {
		fmt.Fprintln(stderr, errs.dim(fmt.Sprintf("witnessed: %d served fresh, %d executed, %d uncacheable",
			res.GetTestsServed(), res.GetTestsExecuted(), res.GetTestsUncacheable())))
		renderInvocationWitnesses(stderr, res.GetInvocationWitnesses())
		if outside := res.GetTestsOutsidePolicy(); outside > 0 {
			fmt.Fprintln(stderr, errs.dim(fmt.Sprintf("outside the witness-eligible selection: %d", outside)))
		}
		renderReasonHistogram(stderr, "re-executed", res.GetExecutedReasons())
		renderUncacheableHistogram(stderr, res.GetUncacheableReasons())
		if d := res.GetWitnessPublicationDegraded(); d != "" {
			fmt.Fprintln(stderr, errs.dim("freshness degraded: "+d))
		}
		for _, d := range res.GetWitnessDiagnostics() {
			fmt.Fprintf(stderr, "%s\n%s", errs.red(views.DiagnosticHeading(d)), d.GetOutput())
			if d.GetTruncated() {
				fmt.Fprintln(stderr, errs.dim("(output truncated)"))
			}
		}
	}
	if p := res.GetWitnessSelectionProblem(); p != "" {
		// On every evidence form, red though it does not itself fail the
		// verdict — with zero behavior bindings the tree can pass while
		// the selection cannot witness anything: the cause stated once,
		// the rows red solely on that boundary carrying the class below
		// (REQ-check-witness-selection).
		fmt.Fprintln(stderr, errs.red(p))
	}
	for _, p := range res.GetVerify().GetProblems() {
		fmt.Fprintln(stderr, errs.red(p.GetPath()+": "+p.GetMessage()))
	}
	cov := res.GetCoverage()
	rows := coverage.RedRows(res)
	// The human account is unbounded: every red row the one ladder
	// classified prints, and a row restating a result-level cause (the
	// witness-selection diagnostic, the scoped pass) carries that class
	// on the row — the bounded projections fold such rows to a count
	// behind the cause; here the cause line stands once above and the
	// row names its class (REQ-check-witness-selection).
	for _, r := range rows {
		reason := ""
		if len(r.Reasons) > 0 {
			reason = "  " + out.dim(r.Reasons[0])
			if len(r.Reasons) > 1 {
				reason += out.dim(fmt.Sprintf(" (+%d more)", len(r.Reasons)-1))
			}
		}
		fmt.Fprintf(stdout, "  %-9s %s%s%s\n", out.yellow(r.Bucket), r.Id, redFoldMark(out, r.Fold), reason)
	}
	folds := map[string]coverage.RedFold{}
	for _, r := range rows {
		folds[r.Id] = r.Fold
	}
	for _, v := range cov.GetViolations() {
		if folds[v] == coverage.RedScopeBlocked {
			// Red solely on the scope boundary: deliberately not
			// executed, excluded from the scoped verdict.
			fmt.Fprintf(stderr, "%s\n", errs.dim("scope-blocked: "+v+" was not executed on this scoped pass"))
			continue
		}
		fmt.Fprintf(stderr, "%s %s is red and no gap excuses it\n", errs.red("violation:"), errs.bold(v))
	}
	for _, path := range res.GetPruneResidue() {
		fmt.Fprintf(stderr, "%s resolved gap lingers: %s — run %s\n", errs.red("prune residue:"), path, errs.bold(remedy.Prune(false)))
	}
	switch {
	case res.GetScopePartial() && res.GetPassed():
		fmt.Fprintln(stdout, out.green("check: pass (partial - scoped to "+strings.Join(res.GetScopeIds(), ", ")+")"))
	case res.GetScopePartial():
		fmt.Fprintln(stdout, out.red("check: fail (partial - scoped to "+strings.Join(res.GetScopeIds(), ", ")+")"))
	case res.GetPassed():
		fmt.Fprintln(stdout, out.green("check: pass"))
	default:
		fmt.Fprintln(stdout, out.red("check: fail"))
	}
}

// renderUncacheableHistogram aggregates the per-test uncacheable reasons
// into a bounded frequency view: the diagnosis instrument for a cache
// that will not warm, without a per-test flood — the full attribution
// rides the machine result.
func renderUncacheableHistogram(stderr io.Writer, reasons map[string]string) {
	renderReasonHistogram(stderr, "uncacheable", reasons)
}

func renderInvocationWitnesses(stderr io.Writer, invs []*stipulatorv1.InvocationWitnessAccount) {
	errs := styleFor(stderr)
	for _, inv := range invs {
		role := "non-granting"
		if inv.GetWitnessEligible() {
			if inv.GetPlainWitness() {
				role = "plain-witness"
			} else {
				role = "witness-eligible"
			}
		}
		fmt.Fprintln(stderr, errs.dim(fmt.Sprintf("  invocation %q (%s): %d served, %d executed, %d uncacheable, %d no-outcome",
			inv.GetInvocation(), role, inv.GetServed(), inv.GetExecuted(), inv.GetUncacheable(), inv.GetNoOutcome())))
	}
}

// renderReasonHistogram is the shared bounded frequency view over one
// per-test reason map.
func renderReasonHistogram(stderr io.Writer, class string, reasons map[string]string) {
	if len(reasons) == 0 {
		return
	}
	errs := styleFor(stderr)
	entries := verify.ReasonHistogram(reasons)
	const shown = 8
	for i, e := range entries {
		if i == shown {
			rest := 0
			for _, r := range entries[shown:] {
				rest += r.N
			}
			fmt.Fprintln(stderr, errs.dim(fmt.Sprintf("  ... and %d more across %d reasons", rest, len(entries)-shown)))
			break
		}
		fmt.Fprintln(stderr, errs.dim(fmt.Sprintf("  %4d  %s: %s", e.N, class, e.Why)))
	}
}

// redFoldMark names the class the one ladder assigned to a red row that
// restates a result-level cause; empty for a red of its own.
func redFoldMark(st styler, f coverage.RedFold) string {
	switch f {
	case coverage.RedPolicyBlocked:
		return st.dim(" (policy-blocked)")
	case coverage.RedScopeBlocked:
		return st.dim(" (scope-blocked)")
	}
	return ""
}
