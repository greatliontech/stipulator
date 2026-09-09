package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	checkpkg "github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/remedy"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

func pruneCmd() *cobra.Command {
	var check, noTest, dangling, storeGC bool
	c := &cobra.Command{
		Use:   remedy.VerbPrune,
		Short: guidanceShort("prune"),
		Long:  guidanceHelp("prune"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Store GC is an identity-liveness fact: the current
			// bound tests-role symbols ARE the obligation universe,
			// matched by exact record-key equality - no symbol
			// parsing - and it runs only as this explicit verb, never
			// opportunistically: an identity absent from THIS tree
			// state may be live on another branch, and silent
			// eviction would undo the variant store's
			// branch-alternation serving (REQ-evidence-store-gc). It
			// judges from records alone - before compilation, so a
			// broken spec never blocks cost cleanup.
			if storeGC {
				if check || dangling || noTest {
					return fmt.Errorf("prune --store composes with no other prune mode or flag")
				}
				store, err := records.Load(os.DirFS(chdir))
				if err != nil {
					return err
				}
				live := map[string]bool{}
				for _, bf := range store.Bindings {
					for _, b := range bf.Set.GetBindings() {
						if b.GetRole() == stipulatorv1.BindingRole_BINDING_ROLE_TESTS {
							live[b.GetSymbol()] = true
						}
					}
				}
				var liveGroup func(string) bool
				// A policy the operation cannot capture keeps every
				// coordinate: cost cleanup never guesses.
				pc, cerr := golang.LoadCapture(cmd.Context(), chdir)
				if cerr == nil {
					digests := golang.LiveGroupDigests(pc)
					liveGroup = func(group string) bool { return digests[group] }
				}
				removed, kept, err := witnesscache.GC(chdir, func(pkg, test string) bool {
					return live[pkg+"."+test]
				}, liveGroup)
				if err != nil {
					return err
				}
				fmt.Printf("store gc: %d record variant(s) removed, %d kept\n", removed, kept)
				// The resolution records beside them: a symbol no
				// binding names and no witness subject carries serves
				// no operation — judged only under a captured policy,
				// since the witness subjects come from it.
				if pc != nil {
					resolutionsRemoved, resolutionsKept, err := golang.GCResolutions(cmd.Context(), chdir, store, pc)
					if err != nil {
						return err
					}
					fmt.Printf("store gc: %d resolution record(s) removed, %d kept\n", resolutionsRemoved, resolutionsKept)
				}
				return nil
			}
			prepared, err := mustPrepare(chdir)
			if err != nil {
				return err
			}
			spec, store, pol := prepared.Spec, prepared.Store, prepared.Coverage
			// Danglingness is a corpus-and-records fact: no witnesses, no
			// symbol resolution, and no verification gate — a dangling gap
			// IS a verification problem, so gating its repair on clean
			// verification would deadlock the repair.
			if dangling {
				prunes := author.PruneDanglingGaps(store, records.HashesOf(spec))
				if check {
					for _, up := range prunes {
						fmt.Printf("%s dangling gap lingers: %s\n", yellow("prunable:"), up.Path)
					}
					if len(prunes) > 0 {
						return fmt.Errorf("prune --dangling --check: %d dangling gaps linger", len(prunes))
					}
					fmt.Println(green("prune: no dangling gaps"))
					return nil
				}
				if err := applyUpdates(chdir, prunes); err != nil {
					return err
				}
				fmt.Printf("prune: %d dangling gaps deleted\n", len(prunes))
				return nil
			}
			// Deletion-only fast path: no gap records means nothing can
			// resolve, so no witness evidence is gathered at all
			// (REQ-gap-resolved-pruned).
			if len(store.Gaps) == 0 {
				if check {
					fmt.Println(green("prune: clean"))
					return nil
				}
				fmt.Println("prune: no gap records - nothing to evaluate")
				return nil
			}
			// A resolved gap is derived from coverage, which is only sound
			// when verification is clean: the record-only half refuses
			// before any child process (REQ-check-preparation).
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
				// Resolution reads the gapped requirements' coverage -
				// and, for a gap with a covered(<id>) landing condition,
				// the condition target's coverage - so the
				// stale-remainder execution narrows to those
				// requirements' bound subjects. A gap id outside the
				// corpus is dangling - never resolvable, owned by the
				// explicit dangling mode - filtered rather than refused;
				// the dangling record still surfaces as a verification
				// problem below.
				scope, gapIds, err := checkpkg.GapScope(spec, store)
				if err != nil {
					return err
				}
				why := fmt.Sprintf("scoped to %d gapped requirements", len(gapIds))
				if testRun, err = witnessRunScoped(cmd.Context(), pc, gb, scope, why); err != nil {
					return err
				}
				// The resolved-record evaluation is pinned to the serving
				// class (REQ-gap-resolved-pruned); the producer's mark
				// makes a wrong witness source a loud refusal.
				if err := verify.ServingClassRequired(testRun); err != nil {
					return err
				}
			}
			fmt.Fprintln(os.Stderr, dim(fmt.Sprintf("evaluated %d gap records", len(store.Gaps))))
			// A resolved gap is derived from coverage, which is only sound
			// when verification is clean: a dangling or stale record could
			// misreport a requirement's bucket and prune a gap that is still
			// load-bearing. Refuse rather than delete on a shaky reading.
			rep := verify.Run(spec, store, map[string]verify.Backend{"go": gb}, testRun)
			if len(rep.Problems) > 0 {
				for _, p := range rep.Problems {
					fmt.Fprintln(os.Stderr, red(p.String()))
				}
				return fmt.Errorf("fix verification problems first")
			}
			cov := coverage.Evaluate(spec, rep, store, !noTest, pol)
			resolved := map[string]bool{}
			for _, g := range cov.Gaps {
				if g.State == coverage.Resolved {
					resolved[g.RequirementId] = true
				}
			}
			prunes := author.PruneResolvedGaps(store, resolved)

			if check {
				for _, up := range prunes {
					fmt.Printf("%s resolved gap lingers: %s\n", yellow("prunable:"), up.Path)
				}
				if len(prunes) > 0 {
					return fmt.Errorf("prune --check: %d resolved gaps linger", len(prunes))
				}
				fmt.Println(green("prune: clean"))
				return nil
			}
			if err := applyUpdates(chdir, prunes); err != nil {
				return err
			}
			fmt.Printf("prune: %d resolved gaps pruned\n", len(prunes))
			return nil
		},
	}
	c.Flags().BoolVar(&check, "check", false, "")
	c.Flags().BoolVar(&noTest, "no-test", false, "")
	c.Flags().BoolVar(&dangling, remedy.FlagDangling, false, "")
	c.Flags().BoolVar(&storeGC, "store", false, "")
	return c
}
