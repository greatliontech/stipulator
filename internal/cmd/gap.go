package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
	checkpkg "github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

func gapCmd() *cobra.Command {
	var reqs, excuseNames []string
	var reason, coveredID, existsID, manual string
	var fired, retract, list bool
	c := &cobra.Command{
		Use:   "gap",
		Short: guidanceShort("gap"),
		Long:  guidanceHelp("gap"),
		RunE: func(cmd *cobra.Command, args []string) error {
			conditioned := coveredID != "" || existsID != "" || manual != "" || reason != "" || len(excuseNames) > 0
			if list {
				if len(reqs) > 0 || conditioned || fired || retract {
					return fmt.Errorf("--list is the read surface and combines with no write flag: editing a gap is re-declaring it")
				}
				return gapListRun(cmd.Context())
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
			lc, err := author.NewLandingCondition(coveredID, existsID, manual, fired)
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
	c.Flags().StringArrayVar(&reqs, "req", nil, "requirement identifier (repeatable; all share the reason and landing condition)")
	c.Flags().StringVar(&reason, "reason", "", "why the gap exists")
	c.Flags().StringVar(&coveredID, "covered", "", "lands when this requirement is covered (self = each requirement's own coverage)")
	c.Flags().StringVar(&existsID, "exists", "", "lands when this requirement exists")
	c.Flags().StringVar(&manual, "manual", "", "lands on this externally judged condition, fired explicitly")
	c.Flags().StringArrayVar(&excuseNames, "excuses", nil, "violation class the gap excuses: uncovered, stale, or broken (repeatable; default uncovered alone)")
	c.Flags().BoolVar(&fired, "fired", false, "mark the manual condition fired (alone: fire existing gaps)")
	c.Flags().BoolVar(&retract, "retract", false, "delete the gap records instead of declaring (dangling records included)")
	c.Flags().BoolVar(&list, "list", false, "list every gap record with its evaluated state (open|due|resolved|dangling); witness evidence gathers only for the gap-relevant requirements")
	registerReqCompletions(c, "req", "covered", "exists")
	return c
}

// gapListRun is the gap surface's read form: every record's declaration
// fields beside its evaluated lifecycle state, the evaluation scoped to
// the gap-relevant requirements exactly as prune's is, with dangling
// records listed rather than refused. It writes nothing; editing a gap
// is re-declaring it.
func gapListRun(ctx context.Context) error {
	spec, err := mustCompile(chdir)
	if err != nil {
		return err
	}
	fsys := os.DirFS(chdir)
	store, err := records.Load(fsys)
	if err != nil {
		return err
	}
	if len(store.Gaps) == 0 {
		fmt.Println("no gap records")
		return nil
	}
	scope, gapIds, err := checkpkg.GapScope(spec, store)
	if err != nil {
		return err
	}
	var testRun *verify.TestRun
	if len(scope) > 0 {
		// An empty scope means no bound witness can move any
		// gap-relevant bucket, so the evaluation is witness-free.
		why := fmt.Sprintf("scoped to %d gapped requirements", len(gapIds))
		if testRun, err = witnessRunScoped(ctx, scope, why); err != nil {
			return err
		}
	}
	backends, err := makeBackends(ctx, chdir)
	if err != nil {
		return err
	}
	rep := verify.Run(spec, store, backends, testRun)
	if len(rep.Problems) > 0 {
		fmt.Fprintln(os.Stderr, yellow(fmt.Sprintf("%d verification problems - evaluated states may misreport; run stipulator verify", len(rep.Problems))))
	}
	manifest, err := corpus.LoadManifest(fsys)
	if err != nil {
		return err
	}
	pol, err := coverage.PolicyFromManifest(manifest)
	if err != nil {
		return err
	}
	cov := coverage.Evaluate(spec, rep, store, testRun != nil, pol)
	row := func(state, id, condition string, manualFired bool, reason string) {
		fired := ""
		if manualFired {
			fired = " fired"
		}
		fmt.Printf("%-9s %s  %s%s  %s\n", state, id, condition, fired, dim(reason))
	}
	known := map[string]bool{}
	for _, r := range spec.GetRequirements() {
		known[r.GetId()] = true
	}
	for _, g := range cov.Gaps {
		// The evaluation's row for an out-of-corpus record is a
		// meaningless Open; the dangling classification below owns it.
		if !known[g.RequirementId] {
			continue
		}
		row(g.State.String(), g.RequirementId, g.Condition, g.Fired, g.Reason)
	}
	// Dangling records are a triage fact, not a refusal: the list is
	// where they are found (their repairs are retraction and the
	// dangling prune).
	for _, gf := range store.Gaps {
		if known[gf.Gap.GetRequirementId()] {
			continue
		}
		row("dangling", gf.Gap.GetRequirementId(), coverage.ConditionText(gf.Gap.GetLands()), gf.Gap.GetLands().GetManual().GetFired(), gf.Gap.GetReason())
	}
	return nil
}
