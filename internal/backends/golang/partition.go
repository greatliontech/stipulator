package golang

import (
	"context"
	"fmt"
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// InvocationSelection is one invocation's discovered obligation set, the
// input to partition conservation.
type InvocationSelection struct {
	// Invocation is the canonical invocation name from the policy record.
	Invocation  string
	Obligations []Obligation
}

// PartitionReports computes the completeness findings of one policy
// against the backend's obligation universe (REQ-policy-conservation):
// every universe obligation selected by no invocation is reported omitted,
// and every obligation — inside the universe or discovered only under an
// invocation's own build selection — selected by more than one invocation
// is reported multiply selected, with the selecting invocation names. An
// obligation selected exactly once yields no finding. Reports are ordered
// by obligation identity; the function is pure so the conservation
// property is quantifiable without a toolchain.
func PartitionReports(universe []Obligation, selections []InvocationSelection) []*stipulatorv1.ObligationReport {
	selectedBy := map[string][]string{}
	for _, sel := range selections {
		seen := map[string]bool{}
		for _, o := range sel.Obligations {
			id := o.ID()
			// One invocation selecting an obligation through several of
			// its own patterns is a single selection, not multiplicity:
			// multiplicity is across invocations, whose separate processes
			// would each produce an outcome for it.
			if seen[id] {
				continue
			}
			seen[id] = true
			selectedBy[id] = append(selectedBy[id], sel.Invocation)
		}
	}
	ids := map[string]bool{}
	var order []string
	for _, o := range universe {
		if !ids[o.ID()] {
			ids[o.ID()] = true
			order = append(order, o.ID())
		}
	}
	for id := range selectedBy {
		if !ids[id] {
			ids[id] = true
			order = append(order, id)
		}
	}
	sort.Strings(order)
	var reports []*stipulatorv1.ObligationReport
	inUniverse := map[string]bool{}
	for _, o := range universe {
		inUniverse[o.ID()] = true
	}
	for _, id := range order {
		invs := selectedBy[id]
		switch {
		case len(invs) == 0 && inUniverse[id]:
			r := &stipulatorv1.ObligationReport{}
			r.SetBackend("go")
			r.SetObligation(id)
			r.SetDisposition(stipulatorv1.ObligationDisposition_OBLIGATION_DISPOSITION_OMITTED)
			reports = append(reports, r)
		case len(invs) > 1:
			names := append([]string(nil), invs...)
			sort.Strings(names)
			r := &stipulatorv1.ObligationReport{}
			r.SetBackend("go")
			r.SetObligation(id)
			r.SetDisposition(stipulatorv1.ObligationDisposition_OBLIGATION_DISPOSITION_MULTIPLY_SELECTED)
			r.SetInvocations(names)
			reports = append(reports, r)
		}
	}
	return reports
}

// ConservationReport discovers the tree's complete obligation universe —
// every workspace member's "./..." under the tree's default build
// selection, the same scope the derived policy declares — and each Go
// invocation's own selection, then reports every omitted or multiply
// selected obligation. Every subprocess it causes runs inside an owned,
// cancellable process boundary.
func ConservationReport(ctx context.Context, dir string, p *stipulatorv1.TestPolicy) ([]*stipulatorv1.ObligationReport, error) {
	universe, err := discoverUniverse(ctx, dir)
	if err != nil {
		return nil, err
	}
	discovered, err := discoverInvocations(ctx, dir, p)
	if err != nil {
		return nil, err
	}
	selections := make([]InvocationSelection, 0, len(discovered))
	for _, d := range discovered {
		selections = append(selections, d.selection)
	}
	return PartitionReports(universe, selections), nil
}

// invocationDiscovery pairs one Go invocation's normalized form with its
// discovered obligation selection.
type invocationDiscovery struct {
	normalized *NormalizedInvocation
	selection  InvocationSelection
}

// discoverInvocations normalizes and discovers every Go invocation of a
// policy in record order.
func discoverInvocations(ctx context.Context, dir string, p *stipulatorv1.TestPolicy) ([]invocationDiscovery, error) {
	var out []invocationDiscovery
	for _, inv := range p.GetInvocations() {
		if inv.GetGo() == nil {
			continue
		}
		n, err := NormalizeInvocation(ctx, dir, inv)
		if err != nil {
			return nil, err
		}
		obs, err := DiscoverInvocation(ctx, n)
		if err != nil {
			return nil, fmt.Errorf("discovering invocation %q: %w", inv.GetName(), err)
		}
		out = append(out, invocationDiscovery{
			normalized: n,
			selection:  InvocationSelection{Invocation: inv.GetName(), Obligations: obs},
		})
	}
	return out, nil
}

// discoverUniverse enumerates the tree's complete obligation universe:
// every workspace member's "./..." under the tree's default build
// selection, deduplicated and member-ordered.
func discoverUniverse(ctx context.Context, dir string) ([]Obligation, error) {
	members, err := policyMembers(dir)
	if err != nil {
		return nil, err
	}
	sort.Strings(members)
	seen := map[string]bool{}
	var universe []Obligation
	for _, m := range members {
		base, err := baselineInvocation(ctx, dir, m)
		if err != nil {
			return nil, err
		}
		obs, err := DiscoverInvocation(ctx, base)
		if err != nil {
			return nil, fmt.Errorf("discovering member %q: %w", m, err)
		}
		for _, o := range obs {
			if !seen[o.ID()] {
				seen[o.ID()] = true
				universe = append(universe, o)
			}
		}
	}
	return universe, nil
}

// baselineInvocation is one workspace member's default-selection scope,
// normalized: the universe against which conservation is judged.
func baselineInvocation(ctx context.Context, dir, member string) (*NormalizedInvocation, error) {
	cfg := &stipulatorv1.GoInvocationConfig{}
	if member != "" {
		cfg.SetModuleRoot(member)
	}
	cfg.SetPackages([]string{"./..."})
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("baseline:" + member)
	inv.SetGo(cfg)
	return NormalizeInvocation(ctx, dir, inv)
}
