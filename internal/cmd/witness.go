package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/greatliontech/gofresh"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verbcore"
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

// witnessRun executes the accepted policy's witnesses — whole when scope
// is nil, the scoped selection otherwise, why naming the scope — and
// announces the run on stderr. The caller owns the verification backend
// and the policy capture: the run consults the backend's classifier and
// the same backend then resolves bindings — one served backend, at most
// one child, per command (REQ-evidence-resolution-freshness). Errors
// pass through unattributed: the verb's own return is the one point
// that names the record path.
func witnessRun(ctx context.Context, pc *golang.Capture, seeding verify.WitnessSeeding, scope map[gofresh.Subject]bool, why string) (*verify.TestRun, error) {
	line := "witnessing: selective execution of the accepted test policy"
	if scope != nil {
		line += ", " + why
	}
	fmt.Fprintln(os.Stderr, dim(line))
	var tr *verify.TestRun
	var err error
	if scope == nil {
		tr, err = runWitnessesPolicy(ctx, pc, seeding)
	} else {
		tr, err = golang.RunWitnessesScoped(ctx, pc, scope, seeding)
	}
	if err != nil {
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
			return nil, nil, err
		}
	}
	symbols, err := golang.OperationSymbols(ctx, store, pc)
	if err != nil {
		return nil, nil, err
	}
	served, err := golang.NewServed(ctx, chdir, symbols)
	if err != nil {
		return nil, nil, err
	}
	return pc, served, nil
}

// runWitnessesPolicy is the one witnessing entry the CLI's whole-tree
// verbs call, held in a variable so an in-process test can observe
// whether a verb reached it — the oracle for "no witness executed
// under a refused vocabulary" that needs no runtime input of its own.
var runWitnessesPolicy = golang.RunWitnessesPolicy

// cliDeps is the verb cores' view of this face: the preparation with
// the CLI's diagnostics, the policy capture, the served backend set,
// and the witness run that announces itself on stderr. A core's error
// reaches the verb unattributed; the verb's return names the record
// path once (withRecordPath).
func cliDeps() verbcore.Deps {
	return verbcore.Deps{
		Prepare: func() (*check.Prepared, error) { return mustPrepare(chdir) },
		Capture: func(ctx context.Context) (*golang.Capture, error) { return golang.LoadCapture(ctx, chdir) },
		Backends: func(ctx context.Context, symbols []string) (map[string]verify.Backend, error) {
			return golang.Backends(ctx, chdir, symbols)
		},
		RunTests: witnessRun,
	}
}
