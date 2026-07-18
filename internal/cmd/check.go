package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/check"
)

func checkCmd() *cobra.Command {
	var jsonOut, quiet bool
	c := &cobra.Command{
		Use:   "check",
		Short: "One pass, one verdict: does this tree pass",
		Long: "Runs the unified check: compiles the corpus, executes the accepted\n" +
			"test policy once, verifies bindings against that execution, evaluates\n" +
			"coverage and gaps, and reports prune residue — one in-process pass,\n" +
			"one verdict. Fails exactly when compilation fails, the accepted\n" +
			"policy record is missing or invalid, verification reports problems,\n" +
			"suite health is unhealthy, a red requirement has no gap naming it,\n" +
			"or a resolved gap record lingers unpruned.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && quiet {
				return fmt.Errorf("give either --json or --quiet")
			}
			if !jsonOut && !quiet {
				fmt.Fprintln(os.Stderr, dim("checking: one execution of the accepted test policy"))
			}
			res, err := check.Run(cmd.Context(), chdir)
			if err != nil {
				return err
			}
			switch {
			case jsonOut:
				out, err := canonicalProtoJSON(res)
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
	return c
}

// canonicalProtoJSON renders a message's deterministic JSON projection:
// the ProtoJSON encoding re-serialized with sorted keys and fixed
// indentation, because protojson.Marshal deliberately randomizes its
// whitespace while machine consumers pin bytes.
func canonicalProtoJSON(m proto.Message) ([]byte, error) {
	b, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
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
