package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/greatliontech/gofresh"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// witnessRun performs the selective witness run of the tree's accepted
// test policy — the one witnessing surface every standalone command
// shares with the unified check (REQ-core-one-execution). A policy
// record problem fails the command carrying the record's path beside the
// loader's guidance, exactly as the check renders it: witness execution
// consumes the accepted policy, never a fallback suite
// (REQ-policy-explicit).
// withRecordPath carries the record's path on a record problem —
// whether the loader found it or the run's discovery did — exactly as
// the check renders it; any other fault passes unchanged.
func withRecordPath(err error) error {
	if errors.Is(err, policy.ErrRecord) {
		return fmt.Errorf("%s: %w", policy.Path, err)
	}
	return err
}

// The caller owns the verification backend and the policy capture: the
// witness run consults the backend's classifier and the same backend
// then resolves bindings — one served backend, at most one child, per
// command (REQ-evidence-resolution-freshness).
func witnessRun(ctx context.Context, pc *golang.Capture, seeding verify.WitnessSeeding) (*verify.TestRun, error) {
	fmt.Fprintln(os.Stderr, dim("witnessing: selective execution of the accepted test policy"))
	tr, err := golang.RunWitnessesPolicy(ctx, pc, seeding)
	if err != nil {
		return nil, withRecordPath(err)
	}
	printWitnessSummary(tr)
	return tr, nil
}

// witnessRunScoped is witnessRun narrowed to a caller-named subject
// scope: fresh records still serve whole-tree, only stale subjects
// inside the scope execute.
func witnessRunScoped(ctx context.Context, pc *golang.Capture, seeding verify.WitnessSeeding, scope map[gofresh.Subject]bool, why string) (*verify.TestRun, error) {
	fmt.Fprintln(os.Stderr, dim("witnessing: selective execution of the accepted test policy, "+why))
	tr, err := golang.RunWitnessesScoped(ctx, pc, scope, seeding)
	if err != nil {
		return nil, withRecordPath(err)
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

// servedBackend prepares a command's verification backend over the
// operation's whole symbol set — the records' bound symbols and the
// captured policy's witness subjects — so resolutions proven fresh serve
// and the owned child opens only for the stale remainder. The record
// path rides a record problem the capture surfaces.
func servedBackend(ctx context.Context, store *records.Store, witnessed bool) (*golang.Capture, *golang.Served, error) {
	var pc *golang.Capture
	if witnessed {
		// Only a witness run consumes the accepted policy: a read-only
		// or --no-test operation resolves its bindings without one
		// (REQ-policy-explicit binds witness execution).
		var err error
		if pc, err = golang.LoadCapture(ctx, chdir); err != nil {
			return nil, nil, withRecordPath(err)
		}
	}
	symbols, err := golang.OperationSymbols(ctx, store, pc)
	if err != nil {
		return nil, nil, withRecordPath(err)
	}
	served, err := golang.NewServed(ctx, chdir, symbols)
	if err != nil {
		return nil, nil, err
	}
	return pc, served, nil
}
