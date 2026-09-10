package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
	checkpkg "github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/remedy"
	"github.com/greatliontech/stipulator/internal/verify"
)

// conditionFlags are the gap verb's condition flags — the ones whose
// presence makes a call a declaration rather than a bare fire,
// retraction, or listing. Every name is a registered flag (pinned by
// test: an unregistered name would read as never present).
var conditionFlags = []string{"covered", "exists", "manual", "reason", "excuses", "contradicted"}

func gapCmd() *cobra.Command {
	var reqs, excuseNames []string
	var reasonVals, coveredVals, existsVals, manualVals []string
	var fired, contradicted, retract, list bool
	c := &cobra.Command{
		Use:   remedy.VerbGap,
		Short: guidanceShort("gap"),
		Long:  guidanceHelp("gap"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A condition flag conditions by PRESENCE: `--reason ""` is a
			// condition the operator spelled, refused where conditions
			// do not apply, never read as absent.
			conditioned := false
			for _, name := range conditionFlags {
				conditioned = conditioned || cmd.Flags().Changed(name)
			}
			// The read surface's guard runs on the raw flag presence,
			// so a --list misuse gets the precise message before any
			// repetition refusal could preempt it.
			if list {
				if len(reqs) > 0 || conditioned || fired || retract {
					return fmt.Errorf("--list is the read surface and combines with no write flag: editing a gap is re-declaring it")
				}
				return gapListRun(cmd.Context())
			}
			// The bulk form shares ONE reason and landing condition
			// across every --req (REQ-gap-bulk), so a repetition of the
			// shared flags expresses a batch this verb cannot form and
			// refuses rather than broadcasting the last value
			// (REQ-evidence-claim-batch's refuse arm).
			reason, err := oneFlag("reason", reasonVals)
			if err != nil {
				return err
			}
			coveredID, err := oneFlag("covered", coveredVals)
			if err != nil {
				return err
			}
			existsID, err := oneFlag("exists", existsVals)
			if err != nil {
				return err
			}
			manual, err := oneFlag("manual", manualVals)
			if err != nil {
				return err
			}
			switch {
			case retract:
				if conditioned || fired {
					return fmt.Errorf("--retract takes only --req: retraction deletes the record, conditions do not apply")
				}
				ups, err := author.RetractGaps(os.DirFS(chdir), reqs)
				if err != nil {
					return err
				}
				return applyUpdates(chdir, ups)
			case fired && manual == "":
				if conditioned {
					return fmt.Errorf("--fired alone fires existing gaps; declaring a new fired gap takes --manual with --fired")
				}
				ups, err := author.FireGaps(os.DirFS(chdir), reqs)
				if err != nil {
					return err
				}
				return applyUpdates(chdir, ups)
			}
			lc, err := author.NewLandingCondition(coveredID, existsID, manual, fired, contradicted)
			if err != nil {
				return err
			}
			excuses, err := author.NewExcuses(excuseNames)
			if err != nil {
				return err
			}
			ups, notes, err := author.Gaps(os.DirFS(chdir), reqs, reason, lc, excuses)
			if err != nil {
				return err
			}
			for _, n := range notes {
				fmt.Println(n)
			}
			return applyUpdates(chdir, ups)
		},
	}
	c.Flags().StringArrayVar(&reqs, remedy.FlagReq, nil, "")
	c.Flags().StringArrayVar(&reasonVals, "reason", nil, "")
	c.Flags().StringArrayVar(&coveredVals, "covered", nil, "")
	c.Flags().StringArrayVar(&existsVals, "exists", nil, "")
	c.Flags().StringArrayVar(&manualVals, "manual", nil, "")
	c.Flags().StringArrayVar(&excuseNames, "excuses", nil, "")
	c.Flags().BoolVar(&fired, "fired", false, "")
	c.Flags().BoolVar(&contradicted, "contradicted", false, "")
	c.Flags().BoolVar(&retract, remedy.FlagRetract, false, "")
	c.Flags().BoolVar(&list, "list", false, "")
	registerReqCompletions(c, "req", "covered", "exists")
	return c
}

// gapListRun is the gap surface's read form: every record's declaration
// fields beside its evaluated lifecycle state, the evaluation scoped to
// the gap-relevant requirements exactly as prune's is, with dangling
// records listed rather than refused. It writes nothing; editing a gap
// is re-declaring it.
func gapListRun(ctx context.Context) error {
	prepared, err := mustPrepare(chdir)
	if err != nil {
		return err
	}
	spec, store, pol := prepared.Spec, prepared.Store, prepared.Coverage
	if len(store.Gaps) == 0 {
		fmt.Println("no gap records")
		return nil
	}
	scope, gapIds, err := checkpkg.GapScope(spec, store)
	if err != nil {
		return err
	}
	// The list is a read surface, not a verification verdict: dangling
	// records are listed rather than refused (REQ-gap-list), so record
	// hygiene warns below and never withholds the witness evidence the
	// other gaps' states derive from — the MCP gap tool evaluates the
	// same way. One owned child serves the run and the resolution.
	pc, gb, err := servedBackend(ctx, store, len(scope) > 0)
	if err != nil {
		return withRecordPath(err)
	}
	defer gb.Close()
	var testRun *verify.TestRun
	if len(scope) > 0 {
		// An empty scope means no bound witness can move any
		// gap-relevant bucket, so the evaluation is witness-free.
		why := fmt.Sprintf("scoped to %d gapped requirements", len(gapIds))
		if testRun, err = witnessRun(ctx, pc, gb, scope, why); err != nil {
			return withRecordPath(err)
		}
	}
	rep := verify.Run(spec, store, map[string]verify.Backend{"go": gb}, testRun)
	if len(rep.Problems) > 0 {
		fmt.Fprintln(os.Stderr, yellow(fmt.Sprintf("%d verification problems - evaluated states may misreport; run %s", len(rep.Problems), remedy.Verify())))
	}
	cov := coverage.Evaluate(spec, rep, store, testRun != nil, pol)
	known := records.HashesOf(spec)
	for _, g := range cov.Gaps {
		// The evaluation's row for an out-of-corpus record is a
		// meaningless Open; the dangling classification below owns it.
		if !known.Known(g.RequirementId) {
			continue
		}
		fmt.Println(gapListLine(g.State.String(), g))
	}
	// Dangling records are a triage fact, not a refusal: the list is
	// where they are found (their repairs are retraction and the
	// dangling prune).
	for _, gf := range store.Gaps {
		if known.Known(gf.Gap.GetRequirementId()) {
			continue
		}
		manual := gf.Gap.GetLands().GetManual()
		fmt.Println(gapListLine("dangling", coverage.Gap{
			RequirementId: gf.Gap.GetRequirementId(), Reason: gf.Gap.GetReason(),
			Condition: coverage.ConditionText(gf.Gap.GetLands()),
			Fired:     manual.GetFired(), Contradicted: manual.GetContradicted(),
		}))
	}
	return nil
}

// gapListLine renders one list row: the state word, the requirement,
// its condition with the declared bits in the one shared order, the
// consent state, and the reason. A suspended excuse is a triage fact
// on the record row itself: the requirement-side reason appears only
// once the requirement is red (REQ-gap-consent).
func gapListLine(state string, g coverage.Gap) string {
	flags := ""
	for _, flag := range records.ManualFlags(g.Contradicted, g.Fired) {
		flags += " " + flag
	}
	consent := ""
	if g.StaleConsent {
		consent = " consent-stale"
	}
	return fmt.Sprintf("%-9s %s  %s%s%s  %s", state, g.RequirementId, g.Condition, flags, consent, dim(g.Reason))
}
