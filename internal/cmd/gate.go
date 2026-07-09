package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/harden"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/views"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func gateCmd() *cobra.Command {
	var reqs []string
	var bucket, filter, pathPrefix, view string
	var jsonOut, quiet bool
	c := &cobra.Command{
		Use:   "gate",
		Short: "Coverage buckets and the gate verdict",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && quiet {
				return fmt.Errorf("give either --json or --quiet")
			}
			spec, err := mustCompile(chdir)
			if err != nil {
				return err
			}
			store, err := records.Load(os.DirFS(chdir))
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, dim("witnessing: go test -json -race ./..."))
			testRun, err := golang.RunTests(chdir)
			if err != nil {
				return err
			}
			backends, err := makeBackends(chdir)
			if err != nil {
				return err
			}
			rep := verify.Run(spec, store, backends, testRun)
			for _, p := range rep.Problems {
				fmt.Fprintln(os.Stderr, red(p.String()))
			}
			if len(rep.Problems) > 0 {
				os.Exit(1)
			}
			manifest, err := corpus.LoadManifest(os.DirFS(chdir))
			if err != nil {
				return err
			}
			pol, err := coverage.PolicyFromManifest(manifest)
			if err != nil {
				return err
			}
			cov := coverage.Evaluate(spec, rep, store, true, pol)
			scope := views.Scope{Ids: reqs, Bucket: bucket, Filter: filter, Path: pathPrefix}
			facts := views.FactsFrom(spec, rep)
			// The covered-but-unhardened reminder is advisory (never gates):
			// a failure to compute it is a warning, not a gate failure.
			reminder, rerr := coverageReminder(chdir, backends, spec, store, cov)
			if rerr != nil {
				fmt.Fprintln(os.Stderr, dim("hardening reminder unavailable: "+rerr.Error()))
			}
			switch {
			case jsonOut:
				m, verr := views.CoverageView(cov, facts, view, scope)
				if verr != nil {
					return verr
				}
				out, verr := mergeReminderJSON(m, reminder)
				if verr != nil {
					return verr
				}
				fmt.Println(out)
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
				printCoverage(&sliced)
				printReminder(reminder)
			}
			if !cov.GatePasses() {
				if !quiet && !jsonOut {
					for _, v := range cov.Violations {
						fmt.Fprintf(os.Stderr, "%s %s is red and no gap names it\n", red("violation:"), bold(v))
					}
					fmt.Println(red("gate: fail"))
				}
				os.Exit(1)
			}
			if !quiet && !jsonOut {
				fmt.Println(green("gate: pass"))
			}
			return nil
		},
	}
	c.Flags().StringArrayVar(&reqs, "req", nil, "scope to requirement identifier (repeatable)")
	c.Flags().StringVar(&bucket, "bucket", "", "scope to one bucket: uncovered, stale, broken, covered, exempt, attested")
	c.Flags().StringVar(&filter, "filter", "", "requirement-id glob, e.g. 'REQ-arch-*'")
	c.Flags().StringVar(&pathPrefix, "path", "", "prefix over declaring document or bound symbols")
	c.Flags().StringVar(&view, "view", "", "JSON view: summary (default), reds, full")
	c.Flags().BoolVar(&jsonOut, "json", false, "machine output: the selected view as JSON")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "exit code only")
	registerReqCompletions(c, "req")
	return c
}

// coverageReminder computes the covered-but-unhardened reminder from the
// already-loaded go backend (never reloading packages), scoped to the
// covered requirements. Advisory only (REQ-harden-coverage-reminder).
func coverageReminder(dir string, backends map[string]verify.Backend, spec *stipulatorv1.Spec, store *records.Store, cov *coverage.Report) (*harden.Reminder, error) {
	gb, ok := backends["go"].(*golang.Backend)
	if !ok {
		return nil, fmt.Errorf("go backend unavailable")
	}
	toolchain, err := golang.Toolchain(dir)
	if err != nil {
		return nil, err
	}
	var covered []string
	for _, r := range cov.Requirements {
		if r.Bucket == coverage.Covered {
			covered = append(covered, r.Id)
		}
	}
	return harden.CoverageReminder(spec, store, gb, toolchain, covered)
}

// mergeReminderJSON marshals the coverage view and folds the hardening
// reminder in under "hardeningReminder".
func mergeReminderJSON(m proto.Message, reminder *harden.Reminder) (string, error) {
	b, err := protojson.Marshal(m)
	if err != nil {
		return "", err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	out["hardeningReminder"] = harden.ReminderMap(reminder)
	merged, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(merged), nil
}

// printReminder renders the covered-but-unhardened tail: hardenable bodies
// first (run harden), then any with no mutation target. Silent when empty.
func printReminder(reminder *harden.Reminder) {
	if reminder == nil || len(reminder.Entries) == 0 {
		return
	}
	hardenable, noTarget := reminder.Counts()
	fmt.Printf("hardening: %s covered %s need harden (run %s)",
		yellow(fmt.Sprint(hardenable)), plural(hardenable, "body", "bodies"), bold("stipulator harden"))
	if noTarget > 0 {
		fmt.Printf(", %d no mutation target", noTarget)
	}
	fmt.Println()
	for _, e := range reminder.Entries {
		tag := "run      "
		if !e.Hardenable {
			tag = "no-target"
		}
		fmt.Printf("  %s %s (%s) %s\n", dim(tag), e.Symbol, strings.Join(e.Requirements, ","), dim(string(e.State)))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// printCoverage renders the human coverage view: red requirements with
// their reasons and gap state merged inline, then one summary line.
func printCoverage(cov *coverage.Report) {
	gapByReq := map[string]coverage.Gap{}
	for _, g := range cov.Gaps {
		gapByReq[g.RequirementId] = g
	}
	for _, o := range cov.PolicyOverrides {
		fmt.Println(dim(o))
	}
	counts := map[coverage.Bucket]int{}
	width := 0
	var reds []coverage.Requirement
	for _, r := range cov.Requirements {
		counts[r.Bucket]++
		if r.Bucket == coverage.Covered || r.Bucket == coverage.Exempt {
			continue
		}
		reds = append(reds, r)
		if len(r.Id) > width {
			width = len(r.Id)
		}
	}
	for _, r := range reds {
		bucket := yellow(r.Bucket.String())
		if r.Bucket == coverage.Broken {
			bucket = red(r.Bucket.String())
		}
		gapNote := red("no gap")
		if r.Bucket == coverage.Attested {
			gapNote = dim("evidence")
		} else if g, ok := gapByReq[r.Id]; ok {
			gapNote = dim("gap " + g.State.String())
		}
		reason := ""
		if len(r.Reasons) > 0 {
			reason = dim(r.Reasons[0])
			if len(r.Reasons) > 1 {
				reason += dim(fmt.Sprintf(" (+%d more)", len(r.Reasons)-1))
			}
		}
		fmt.Printf("  %-9s %-*s  %s  %s\n", bucket, width, r.Id, gapNote, reason)
	}
	fmt.Printf("coverage: %s covered, %s attested, %s uncovered, %s stale, %s broken, %d exempt; gaps: %d\n",
		green(fmt.Sprint(counts[coverage.Covered])), num(counts[coverage.Attested], yellow),
		num(counts[coverage.Uncovered], yellow),
		num(counts[coverage.Stale], yellow), num(counts[coverage.Broken], red),
		counts[coverage.Exempt], len(cov.Gaps))
	if _, prunable := coverage.GapCounts(cov.Gaps, nil); prunable > 0 {
		noun := "gap"
		if prunable > 1 {
			noun = "gaps"
		}
		fmt.Printf("prunable: %d resolved %s — run %s\n", prunable, noun, bold("stipulator prune"))
	}
}
