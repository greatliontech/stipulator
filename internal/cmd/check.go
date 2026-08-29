package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/wire"
)

func checkCmd() *cobra.Command {
	var jsonOut, quiet, full bool
	var ids string
	c := &cobra.Command{
		Use:   "check",
		Short: guidanceShort("check"),
		Long:  guidanceLong("check"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && quiet {
				return fmt.Errorf("give either --json or --quiet")
			}
			if !jsonOut && !quiet {
				if full {
					fmt.Fprintln(os.Stderr, dim("checking: one execution of the accepted test policy"))
				} else {
					fmt.Fprintln(os.Stderr, dim("checking: serving fresh witnesses, executing the stale remainder"))
				}
			}
			res, err := check.Run(cmd.Context(), chdir, full, splitCommaIDs(ids))
			if err != nil {
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
				os.Exit(1)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "machine output: the check result as deterministic JSON")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "exit code only")
	c.Flags().BoolVar(&full, "full", false, "execute the whole accepted policy and judge suite health")
	c.Flags().StringVar(&ids, "ids", "", "comma-separated requirement identifiers scoping the pass: only stale subjects bound to them execute, the verdict is flagged partial")
	return c
}

// renderCheck prints the human view of one check result. Every line is a
// projection of the message — the wire result is the one source, so the
// human and machine surfaces cannot drift.
func renderCheck(stdout, stderr io.Writer, res *stipulatorv1.CheckResult) {
	for _, p := range res.GetCompileProblems() {
		fmt.Fprintln(stderr, red(p.GetPath()+": "+p.GetMessage()))
	}
	if p := res.GetPolicyProblem(); p != nil {
		fmt.Fprintln(stderr, red(p.GetPath()+": "+p.GetMessage()))
	}
	if ex := res.GetExecution(); ex != nil {
		fmt.Fprintln(stderr, dim(fmt.Sprintf("witnessed: %d executed, %d uncacheable",
			res.GetTestsExecuted(), res.GetTestsUncacheable())))
		renderUncacheableHistogram(stderr, res.GetUncacheableReasons())
		if d := res.GetWitnessPublicationDegraded(); d != "" {
			fmt.Fprintln(stderr, dim("freshness publication degraded: "+d))
		}
		for _, d := range ex.GetDiagnostics() {
			fmt.Fprintf(stderr, "%s\n%s", red(diagnosticHeading(d)), d.GetOutput())
			if d.GetTruncated() {
				fmt.Fprintln(stderr, dim("(output truncated)"))
			}
		}
	} else if !res.GetSuiteHealthJudged() && res.GetPolicyProblem() == nil && len(res.GetCompileProblems()) == 0 {
		fmt.Fprintln(stderr, dim(fmt.Sprintf("witnessed: %d served fresh, %d executed, %d uncacheable",
			res.GetTestsServed(), res.GetTestsExecuted(), res.GetTestsUncacheable())))
		if outside := res.GetTestsOutsidePolicy(); outside > 0 {
			fmt.Fprintln(stderr, dim(fmt.Sprintf("outside the witness-eligible selection: %d", outside)))
		}
		if p := res.GetWitnessSelectionProblem(); p != "" {
			// Rendered red though it does not itself fail the verdict: with
			// zero behavior bindings the tree can pass while the selection
			// cannot witness anything - the line warns, the bindings fail.
			fmt.Fprintln(stderr, red(p))
		}
		renderReasonHistogram(stderr, "re-executed", res.GetExecutedReasons())
		renderUncacheableHistogram(stderr, res.GetUncacheableReasons())
		if d := res.GetWitnessPublicationDegraded(); d != "" {
			fmt.Fprintln(stderr, dim("freshness degraded: "+d))
		}
		for _, d := range res.GetWitnessDiagnostics() {
			fmt.Fprintf(stderr, "%s\n%s", red(diagnosticHeading(d)), d.GetOutput())
			if d.GetTruncated() {
				fmt.Fprintln(stderr, dim("(output truncated)"))
			}
		}
	}
	for _, p := range res.GetVerify().GetProblems() {
		fmt.Fprintln(stderr, red(p.GetPath()+": "+p.GetMessage()))
	}
	cov := res.GetCoverage()
	for _, r := range cov.GetRequirements() {
		switch r.GetBucket() {
		case stipulatorv1.Bucket_BUCKET_UNCOVERED, stipulatorv1.Bucket_BUCKET_STALE, stipulatorv1.Bucket_BUCKET_BROKEN:
			reason := ""
			if rs := r.GetReasons(); len(rs) > 0 {
				reason = "  " + dim(rs[0])
				if len(rs) > 1 {
					reason += dim(fmt.Sprintf(" (+%d more)", len(rs)-1))
				}
			}
			fmt.Fprintf(stdout, "  %-9s %s%s\n", yellow(bucketWord(r.GetBucket())), r.GetId(), reason)
		}
	}
	scopeBlocked := map[string]bool{}
	if res.GetScopePartial() {
		for _, r := range cov.GetRequirements() {
			if r.GetScopeBlocked() {
				scopeBlocked[r.GetId()] = true
			}
		}
	}
	for _, v := range cov.GetViolations() {
		if scopeBlocked[v] {
			// Red solely on the scope boundary: deliberately not
			// executed, excluded from the scoped verdict.
			fmt.Fprintf(stderr, "%s\n", dim("scope-blocked: "+v+" was not executed on this scoped pass"))
			continue
		}
		fmt.Fprintf(stderr, "%s %s is red and no gap excuses it\n", red("violation:"), bold(v))
	}
	for _, path := range res.GetPruneResidue() {
		fmt.Fprintf(stderr, "%s resolved gap lingers: %s — run %s\n", red("prune residue:"), path, bold("stipulator prune"))
	}
	switch {
	case res.GetScopePartial() && res.GetPassed():
		fmt.Fprintln(stdout, green("check: pass (partial - scoped to "+strings.Join(res.GetScopeIds(), ", ")+")"))
	case res.GetScopePartial():
		fmt.Fprintln(stdout, red("check: fail (partial - scoped to "+strings.Join(res.GetScopeIds(), ", ")+")"))
	case res.GetPassed():
		fmt.Fprintln(stdout, green("check: pass"))
	default:
		fmt.Fprintln(stdout, red("check: fail"))
	}
}

// diagnosticHeading names one failure diagnostic's unit and disposition.
// A degraded execution is named distinctly from an assertion failure:
// conflating them would leave an environment-induced failure and a real
// regression indistinguishable.
func diagnosticHeading(d *stipulatorv1.FailureDiagnostic) string {
	subject := d.GetInvocation()
	if p := d.GetPackage(); p != "" {
		subject = p
	}
	if t := d.GetTest(); t != "" {
		subject = d.GetPackage() + "." + t
	}
	switch d.GetDisposition() {
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED:
		return "degraded: " + subject
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED:
		return "build failed: " + subject
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT:
		return "timeout: " + subject
	default:
		return "failed: " + subject
	}
}

// renderUncacheableHistogram aggregates the per-test uncacheable reasons
// into a bounded frequency view: the diagnosis instrument for a cache
// that will not warm, without a per-test flood — the full attribution
// rides the machine result.
func renderUncacheableHistogram(stderr io.Writer, reasons map[string]string) {
	renderReasonHistogram(stderr, "uncacheable", reasons)
}

// renderReasonHistogram is the shared bounded frequency view over one
// per-test reason map.
func renderReasonHistogram(stderr io.Writer, class string, reasons map[string]string) {
	if len(reasons) == 0 {
		return
	}
	counts := map[string]int{}
	for _, why := range reasons {
		counts[why]++
	}
	type entry struct {
		why string
		n   int
	}
	entries := make([]entry, 0, len(counts))
	for why, n := range counts {
		entries = append(entries, entry{why, n})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].n != entries[j].n {
			return entries[i].n > entries[j].n
		}
		return entries[i].why < entries[j].why
	})
	const shown = 8
	for i, e := range entries {
		if i == shown {
			rest := 0
			for _, r := range entries[shown:] {
				rest += r.n
			}
			fmt.Fprintln(stderr, dim(fmt.Sprintf("  ... and %d more across %d reasons", rest, len(entries)-shown)))
			break
		}
		fmt.Fprintln(stderr, dim(fmt.Sprintf("  %4d  %s: %s", e.n, class, e.why)))
	}
}

func bucketWord(b stipulatorv1.Bucket) string {
	switch b {
	case stipulatorv1.Bucket_BUCKET_STALE:
		return "stale"
	case stipulatorv1.Bucket_BUCKET_BROKEN:
		return "broken"
	}
	return "uncovered"
}

// splitCommaIDs parses the --ids flag: empty means no scope, and blank
// entries are dropped rather than refused - the shell's trailing comma
// is not a typo worth a failed pass.
func splitCommaIDs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
