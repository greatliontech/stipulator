package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/wire"
)

func checkCmd() *cobra.Command {
	var jsonOut, quiet, full bool
	c := &cobra.Command{
		Use:   "check",
		Short: "One pass, one verdict: does this tree pass",
		Long: "Runs the unified check: compiles the corpus, takes witness evidence\n" +
			"— served from proven-fresh records with selective execution of the\n" +
			"stale remainder by default, or one whole execution of the accepted\n" +
			"test policy under --full — verifies bindings against that evidence,\n" +
			"evaluates coverage and gaps, and reports prune residue: one\n" +
			"in-process pass, one verdict. Fails exactly when compilation fails,\n" +
			"the accepted policy record is missing or invalid, verification\n" +
			"reports problems, a red requirement has no gap naming it, or a\n" +
			"resolved gap record lingers unpruned; --full additionally fails on\n" +
			"unhealthy suite health, which only whole execution can judge.",
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
			res, err := check.Run(cmd.Context(), chdir, full)
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
	for _, v := range cov.GetViolations() {
		fmt.Fprintf(stderr, "%s %s is red and no gap names it\n", red("violation:"), bold(v))
	}
	for _, path := range res.GetPruneResidue() {
		fmt.Fprintf(stderr, "%s resolved gap lingers: %s — run %s\n", red("prune residue:"), path, bold("stipulator prune"))
	}
	if res.GetPassed() {
		fmt.Fprintln(stdout, green("check: pass"))
	} else {
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

func bucketWord(b stipulatorv1.Bucket) string {
	switch b {
	case stipulatorv1.Bucket_BUCKET_STALE:
		return "stale"
	case stipulatorv1.Bucket_BUCKET_BROKEN:
		return "broken"
	}
	return "uncovered"
}
