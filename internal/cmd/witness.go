package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/verify"
)

// witnessRun performs the selective witness run of the tree's accepted
// test policy — the one witnessing surface every standalone command
// shares with the unified check (REQ-core-one-execution). A policy
// record problem fails the command carrying the record's path beside the
// loader's guidance, exactly as the check renders it: witness execution
// consumes the accepted policy, never a fallback suite
// (REQ-policy-explicit).
func witnessRun(ctx context.Context) (*verify.TestRun, error) {
	fmt.Fprintln(os.Stderr, dim("witnessing: selective execution of the accepted test policy"))
	tr, err := golang.RunWitnesses(ctx, chdir)
	if err != nil {
		if errors.Is(err, policy.ErrRecord) {
			return nil, fmt.Errorf("%s: %w", policy.Path, err)
		}
		return nil, err
	}
	printWitnessSummary(tr)
	return tr, nil
}

// printWitnessSummary renders one witness run's shared stderr surface:
// the run/served/uncacheable/outside-policy counts, the degraded reason
// when the freshness path faulted, each failed test's retained output,
// and the package-level diagnostic rows no single test owns — the denied
// subjects' visibility story. Keys render sorted so identical runs
// render identically (REQ-core-determinism).
func printWitnessSummary(tr *verify.TestRun) {
	if tr.Ran+tr.Fresh+tr.OutsidePolicy > 0 {
		fmt.Fprintln(os.Stderr, dim(fmt.Sprintf("witnessed: %d ran, %d served fresh, %d uncacheable, %d outside policy",
			tr.Ran, tr.Fresh, tr.Uncached, tr.OutsidePolicy)))
	}
	if tr.Degraded != "" {
		fmt.Fprintln(os.Stderr, dim("freshness degraded: "+tr.Degraded))
	}
	renderReasonHistogram(os.Stderr, "re-executed", tr.ExecutedReasons)
	renderUncacheableHistogram(os.Stderr, tr.UncacheableReasons)
	for _, key := range sortedKeys(tr.Failures) {
		fmt.Fprintf(os.Stderr, "%s\n%s", red("witness failed: "+key), tr.Failures[key])
	}
	for _, d := range tr.Diagnostics {
		if d.GetTest() != "" {
			continue
		}
		fmt.Fprintf(os.Stderr, "%s\n%s", red("witness denied: "+d.GetPackage()), d.GetOutput())
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
