package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/verbcore"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/views"
)

// withRecordPath names the accepted policy's path on a record error —
// the verb's one attribution point.
func withRecordPath(err error) error {
	if errors.Is(err, policy.ErrRecord) {
		return fmt.Errorf("%s: %w", policy.Path, err)
	}
	return err
}

// witnessRun executes the accepted policy's witnesses — whole when scope
// is nil, the scoped selection otherwise, why naming the scope — and
// announces the run on w. The caller owns the verification backend
// and the policy capture: the run consults the backend's classifier and
// the same backend then resolves bindings — one served backend, at most
// one child, per command (REQ-evidence-resolution-freshness-quiesce). Errors
// pass through unattributed: the verb's own return is the one point
// that names the record path.
func witnessRun(ctx context.Context, w io.Writer, pc *golang.Capture, seeding verify.WitnessSeeding, scope map[gofresh.Subject]bool, why string) (*verify.TestRun, error) {
	st := styleFor(w)
	line := "witnessing: selective execution of the accepted test policy"
	if scope != nil {
		line += ", " + why
	}
	fmt.Fprintln(w, st.dim(line))
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
	printWitnessSummary(w, tr)
	return tr, nil
}

// printWitnessSummary renders one witness run's shared human surface:
// the run/served/uncacheable/outside-policy counts, the degraded reason
// when the freshness path faulted, each failed test's retained output,
// and the package-level diagnostic rows no single test owns — the denied
// subjects' visibility story. Keys render sorted so identical runs
// render identically (REQ-core-determinism).
func printWitnessSummary(w io.Writer, tr *verify.TestRun) {
	st := styleFor(w)
	renderWitnessCacheLoad(w, tr.WitnessCacheLoad)
	if tr.Ran+tr.Fresh+tr.OutsidePolicy > 0 {
		fmt.Fprintln(w, st.dim(fmt.Sprintf("witnessed: %d ran, %d served fresh, %d uncacheable, %d outside policy",
			tr.Ran, tr.Fresh, tr.Uncached, tr.OutsidePolicy)))
	}
	if tr.Degraded != "" {
		fmt.Fprintln(w, st.dim("freshness degraded: "+tr.Degraded))
	}
	renderReasonHistogram(w, "re-executed", tr.ExecutedReasons)
	renderUncacheableHistogram(w, tr.UncacheableReasons)
	for _, key := range sortedKeys(tr.Failures) {
		fmt.Fprintf(w, "%s\n%s", st.red("witness failed: "+key), tr.Failures[key])
	}
	for _, d := range tr.Diagnostics {
		if d.GetTest() != "" {
			continue
		}
		fmt.Fprintf(w, "%s\n%s", st.red("witness denied: "+d.GetPackage()), d.GetOutput())
	}
}

// renderWitnessCacheLoad uses the same bounded projection as MCP summaries.
// Advisory refusals never acquire the styling or counts of failed witnesses.
func renderWitnessCacheLoad(w io.Writer, account *stipulatorv1.WitnessCacheLoadAccount) {
	a := views.WitnessCacheLoadSummary(account)
	if a == nil {
		return
	}
	partial := ""
	if a.GetIncomplete() {
		partial = "; incomplete"
	}
	fmt.Fprintf(w, "witness cache: %d observed refusals%s\n", uint64(len(a.GetRefusals()))+a.GetOmitted(), partial)
	if a.GetProblem() != "" {
		fmt.Fprintf(w, "  %s%s\n", a.GetProblem(), truncationNote(a.GetProblemTruncated()))
	}
	for _, r := range a.GetRefusals() {
		fmt.Fprintf(w, "  %s%s: %s: %s%s\n", r.GetFile(), truncationNote(r.GetFilenameTruncated()), r.GetCause(), r.GetDetail(), truncationNote(r.GetDetailTruncated()))
		if c := r.GetCoordinate(); c != nil {
			fmt.Fprintf(w, "    group=%s package=%s test=%s\n", c.GetGroup(), c.GetPackage(), c.GetTest())
		} else if r.GetCoordinateOmitted() {
			fmt.Fprintln(w, "    coordinate omitted (oversize)")
		}
	}
	if a.GetOmitted() > 0 {
		fmt.Fprintf(w, "  %d further refusals omitted\n", a.GetOmitted())
	}
}

func truncationNote(truncated bool) string {
	if truncated {
		return " (truncated)"
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// runWitnessesPolicy is the one witnessing entry the CLI's whole-tree
// verbs call, held in a variable so an in-process test can observe
// whether a verb reached it — the oracle for "no witness executed
// under a refused vocabulary" that needs no runtime input of its own.
var runWitnessesPolicy = golang.RunWitnessesPolicy

// cliDeps is the verb cores' view of this face: preparation diagnostics
// and witness rendering on w, the policy capture, and the served backend
// set. A core's error
// reaches the verb unattributed; the verb's return names the record
// path once (withRecordPath).
func cliDeps(w io.Writer) verbcore.Deps {
	return verbcore.Deps{
		Prepare: func() (*check.Prepared, error) { return mustPrepare(w, chdir) },
		Capture: func(ctx context.Context) (*golang.Capture, error) { return golang.LoadCapture(ctx, chdir) },
		Backends: func(ctx context.Context, symbols []string) (map[string]verify.Backend, error) {
			return servingBackends(ctx, chdir, symbols)
		},
		RunTests: func(ctx context.Context, pc *golang.Capture, seeding verify.WitnessSeeding, scope map[gofresh.Subject]bool, why string) (*verify.TestRun, error) {
			return witnessRun(ctx, w, pc, seeding, scope, why)
		},
	}
}
