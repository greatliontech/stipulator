package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/remedy"
	"github.com/greatliontech/stipulator/internal/verbcore"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/verifyrun"
	"github.com/greatliontech/stipulator/internal/views"
	"github.com/greatliontech/stipulator/internal/wire"
)

// gateGapNote spells a red requirement's standing gap on the gate's
// row: its lifecycle state, and the contradicted class named beside it
// — a known debt with a trigger reads apart from a coverage hole on
// the triage surface too (REQ-gap-lifecycle).
func gateGapNote(g coverage.Gap) string {
	note := "gap " + g.State.String()
	if g.Contradicted {
		note += ", contradicted"
	}
	return note
}

func gateCmd() *cobra.Command {
	var reqs []string
	var bucket, filter, pathPrefix, view string
	var jsonOut, quiet bool
	c := &cobra.Command{
		Use:   "gate",
		Short: guidanceShort("gate"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && quiet {
				return fmt.Errorf("give either --json or --quiet")
			}
			// The caller's vocabulary is judged before anything compiles;
			// the exact ids, the hygiene, and the coverage policy refuse
			// inside the verification pass, before any child process
			// (REQ-check-preparation).
			ids, err := verbcore.SplitIDLists(reqs)
			if err != nil {
				return err
			}
			scope := views.Scope{Ids: ids, Bucket: bucket, Filter: filter, Path: pathPrefix}
			if err := validateScoped(scope, views.ValidateCoverageView, view); err != nil {
				return err
			}
			var humanOutput io.Writer = os.Stderr
			if quiet {
				humanOutput = io.Discard
			}
			prepared, rep, _, err := verifyrun.Run(cmd.Context(), cliDeps(humanOutput), false, scope.Ids)
			if err != nil {
				renderNotices(humanOutput, verify.NoticesOf(err))
				return withRecordPath(err)
			}
			// Every problem refuses — the record-hygiene half and a
			// problem the witness run itself found alike: a coverage
			// judgment over a problem-bearing record is unsound, on
			// this face as on the MCP (REQ-check-preparation). The
			// record-only report re-derives the hygiene problems, so
			// this one call covers both halves.
			// The serving account first: the close that published has
			// run, and a refusal below must not hide it
			// (REQ-evidence-resolution-freshness-account).
			if !quiet {
				renderNotices(os.Stderr, rep.ResolutionNotices)
			}
			if err := refuseProblems(rep.Problems); err != nil {
				return err
			}
			spec, store, pol := prepared.Spec, prepared.Store, prepared.Coverage
			cov := coverage.Evaluate(spec, rep, store, true, pol)
			facts := views.FactsFrom(spec, rep)
			switch {
			case jsonOut:
				m, verr := views.CoverageView(cov, facts, view, scope)
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
			case quiet:
				// Exit code only, for CI.
			default:
				rows, verr := views.FilterRows(cov, facts, scope)
				if verr != nil {
					return verr
				}
				var keep map[string]bool
				if !scope.Empty() {
					keep = make(map[string]bool, len(rows))
					for _, r := range rows {
						keep[r.Id] = true
					}
				}
				sliced := views.ScopeReport(cov, rows, keep)
				printCoverage(os.Stdout, &sliced)
			}
			if !cov.GatePasses() {
				if !quiet && !jsonOut {
					errs := errStyle()
					for _, v := range cov.Violations {
						fmt.Fprintf(os.Stderr, "%s %s is red and no gap excuses it\n", errs.red("violation:"), errs.bold(v))
					}
					fmt.Println(outStyle().red("gate: fail"))
				}
				return exitStatus(1)
			}
			if !quiet && !jsonOut {
				fmt.Println(outStyle().green("gate: pass"))
			}
			return nil
		},
	}
	c.Flags().StringArrayVar(&reqs, "req", nil, "")
	c.Flags().StringVar(&bucket, "bucket", "", "")
	c.Flags().StringVar(&filter, "filter", "", "")
	c.Flags().StringVar(&pathPrefix, "path", "", "")
	c.Flags().StringVar(&view, "view", "", "")
	c.Flags().BoolVar(&jsonOut, "json", false, "")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "")
	registerReqCompletions(c, "req")
	return c
}

// printCoverage renders the human coverage view: red requirements with
// their reasons and gap state merged inline, then one summary line.
func printCoverage(w io.Writer, cov *coverage.Report) {
	st := styleFor(w)
	gapByReq := map[string]coverage.Gap{}
	for _, g := range cov.Gaps {
		gapByReq[g.RequirementId] = g
	}
	for _, o := range cov.PolicyOverrides {
		fmt.Fprintln(w, st.dim(o))
	}
	counts := map[coverage.Bucket]int{}
	width := 0
	var reds []coverage.Requirement
	for _, r := range cov.Requirements {
		counts[r.Bucket]++
		// The rows are the red membership every surface consults plus the
		// attested rows, which appear distinctly in every coverage output
		// as the weakest evidence (REQ-evidence-attestation) — stated as
		// the set it is, so a bucket added to the ladder lists by its own
		// predicate, never by not being covered.
		if !r.Bucket.Red() && r.Bucket != coverage.Attested {
			continue
		}
		reds = append(reds, r)
		if len(r.Id) > width {
			width = len(r.Id)
		}
	}
	for _, r := range reds {
		bucket := st.yellow(r.Bucket.String())
		if r.Bucket == coverage.Broken {
			bucket = st.red(r.Bucket.String())
		}
		gapNote := st.red("no gap")
		if r.Bucket == coverage.Attested {
			gapNote = st.dim("evidence")
		} else if g, ok := gapByReq[r.Id]; ok {
			gapNote = st.dim(gateGapNote(g))
		}
		reason := ""
		if len(r.Reasons) > 0 {
			reason = st.dim(r.Reasons[0])
			if len(r.Reasons) > 1 {
				reason += st.dim(fmt.Sprintf(" (+%d more)", len(r.Reasons)-1))
			}
		}
		fmt.Fprintf(w, "  %-9s %-*s  %s  %s\n", bucket, width, r.Id, gapNote, reason)
	}
	tally := coverage.GapCounts(cov.Gaps, nil)
	prunable := tally.Resolved
	pointers := ""
	if n := coverage.DanglingPointerCount(cov.DanglingPointers, nil); n > 0 {
		pointers = fmt.Sprintf("; pointers: %s dangling", st.red(fmt.Sprint(n)))
	}
	fmt.Fprintf(w, "coverage: %s covered, %s attested, %s uncovered, %s partial, %s stale, %s broken, %d exempt; gaps: %s%s\n",
		st.green(fmt.Sprint(counts[coverage.Covered])), st.num(counts[coverage.Attested], st.yellow),
		st.num(counts[coverage.Uncovered], st.yellow), st.num(counts[coverage.Partial], st.yellow),
		st.num(counts[coverage.Stale], st.yellow), st.num(counts[coverage.Broken], st.red),
		counts[coverage.Exempt], tally.Text(), pointers)
	if prunable > 0 {
		noun := "gap"
		if prunable > 1 {
			noun = "gaps"
		}
		fmt.Fprintf(w, "prunable: %d resolved %s — run %s\n", prunable, noun, st.bold(remedy.Prune(false)))
	}
}
