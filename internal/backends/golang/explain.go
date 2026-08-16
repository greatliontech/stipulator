package golang

import (
	"context"
	"sort"
	"strings"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// ExplainDynamicState derives the refusal chain for a dynamic-state
// culprit a witness reason names - the variable varName declared in
// package pkgPath - against the same policy-scoped views the
// verdicts derive over (gofresh's explain contract). Groups are
// tried in deterministic group-key order; the first group whose view
// yields a chain answers, named by its member invocations (sorted,
// comma-joined) so a caller holding a reason from a different view
// can see the mismatch. A culprit no group's view knows yields an
// empty chain, an empty view name, and no error.
func ExplainDynamicState(ctx context.Context, dir string, p *stipulatorv1.TestPolicy, pkgPath, varName string) (gofresh.Chain, string, error) {
	pc, err := capturePolicy(ctx, dir, p)
	if err != nil {
		return gofresh.Chain{}, "", err
	}
	for _, g := range pc.groups {
		subjects := groupSubjects(g)
		if len(subjects) == 0 {
			continue
		}
		engine, err := groupEngine(ctx, dir, g)
		if err != nil {
			return gofresh.Chain{}, "", err
		}
		view, err := engine.NewView(ctx, subjects, dir)
		if err != nil {
			return gofresh.Chain{}, "", err
		}
		chain, err := view.ExplainDynamicState(ctx, pkgPath, varName)
		if err != nil {
			return gofresh.Chain{}, "", err
		}
		if chain.Arm != "" {
			names := append([]string(nil), g.invs...)
			sort.Strings(names)
			return chain, strings.Join(names, ","), nil
		}
	}
	return gofresh.Chain{}, "", nil
}
