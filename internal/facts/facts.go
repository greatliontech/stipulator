// Package facts derives code-context facts for work dissemination: seed
// symbols from the spec neighborhood's bindings, and candidate work
// partitions from closure connectivity and slice overlap.
//
// Everything here is a derived report — computed on demand, never stored —
// and facts only: selection, ordering, and budgets belong to consumers.
package facts

import (
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/bundle"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Seed is a bound symbol seeding code context for a requirement set.
type Seed struct {
	Requirement string
	Backend     string
	Symbol      string
	Role        stipulatorv1.BindingRole
}

// Seeds derives the seed symbols for a requirement set: the bindings of
// the set's closure. A greenfield requirement has no bindings of its own,
// but its spec neighborhood does — that is the point.
func Seeds(spec *stipulatorv1.Spec, store *records.Store, ids []string) ([]Seed, error) {
	b, err := bundle.Compute(spec, ids)
	if err != nil {
		return nil, err
	}
	inClosure := map[string]bool{}
	for _, r := range b.GetRequirements() {
		inClosure[r.GetId()] = true
	}
	seen := map[string]bool{}
	var out []Seed
	for _, bf := range store.Bindings {
		for _, bind := range bf.Set.GetBindings() {
			if !inClosure[bind.GetRequirementId()] {
				continue
			}
			key := bind.GetBackend() + "|" + bind.GetSymbol() + "|" + bind.GetRequirementId()
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Seed{
				Requirement: bind.GetRequirementId(),
				Backend:     bind.GetBackend(),
				Symbol:      bind.GetSymbol(),
				Role:        bind.GetRole(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Requirement != b.Requirement {
			return a.Requirement < b.Requirement
		}
		return a.Symbol < b.Symbol
	})
	return out, nil
}

// Context returns seeds plus the slice of declarations their go symbols
// reach, through the backends that can slice.
func Context(spec *stipulatorv1.Spec, store *records.Store, backends map[string]verify.Backend, ids []string) ([]Seed, []verify.Decl, error) {
	seeds, err := Seeds(spec, store, ids)
	if err != nil {
		return nil, nil, err
	}
	perBackend := map[string][]string{}
	for _, s := range seeds {
		perBackend[s.Backend] = append(perBackend[s.Backend], s.Symbol)
	}
	var decls []verify.Decl
	names := make([]string, 0, len(perBackend))
	for name := range perBackend {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		slicer, ok := backends[name].(verify.Slicer)
		if !ok {
			continue
		}
		ds, err := slicer.Slice(perBackend[name])
		if err != nil {
			return nil, nil, err
		}
		decls = append(decls, ds...)
	}
	return seeds, decls, nil
}

// Component is one candidate work unit.
type Component struct {
	Requirements []string
	Seeds        []Seed
	Packages     []string
}

// Overlap reports two components sharing packages.
type Overlap struct {
	A, B     int
	Packages []string
}

// Report is the candidate-partition derivation.
type Report struct {
	Components []Component
	Overlaps   []Overlap
}

// Partitions groups a requirement set into connected components of
// intersecting closures, each with its seeds and touched packages, and
// reports pairwise package overlaps.
func Partitions(spec *stipulatorv1.Spec, store *records.Store, backends map[string]verify.Backend, ids []string) (*Report, error) {
	sort.Strings(ids)
	closures := make([]map[string]bool, len(ids))
	for i, id := range ids {
		b, err := bundle.Compute(spec, []string{id})
		if err != nil {
			return nil, err
		}
		set := map[string]bool{}
		for _, r := range b.GetRequirements() {
			set[r.GetId()] = true
		}
		closures[i] = set
	}

	parent := make([]int, len(ids))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if intersects(closures[i], closures[j]) {
				parent[find(i)] = find(j)
			}
		}
	}
	groups := map[int][]string{}
	for i, id := range ids {
		root := find(i)
		groups[root] = append(groups[root], id)
	}
	roots := make([]int, 0, len(groups))
	for r := range groups {
		roots = append(roots, r)
	}
	sort.Ints(roots)

	rep := &Report{}
	for _, r := range roots {
		reqs := groups[r]
		sort.Strings(reqs)
		seeds, decls, err := Context(spec, store, backends, reqs)
		if err != nil {
			return nil, err
		}
		pkgSet := map[string]bool{}
		for _, d := range decls {
			if d.Package != "" {
				pkgSet[d.Package] = true
			}
		}
		pkgs := make([]string, 0, len(pkgSet))
		for p := range pkgSet {
			pkgs = append(pkgs, p)
		}
		sort.Strings(pkgs)
		rep.Components = append(rep.Components, Component{
			Requirements: reqs, Seeds: seeds, Packages: pkgs,
		})
	}

	for i := 0; i < len(rep.Components); i++ {
		for j := i + 1; j < len(rep.Components); j++ {
			shared := sharedStrings(rep.Components[i].Packages, rep.Components[j].Packages)
			if len(shared) > 0 {
				rep.Overlaps = append(rep.Overlaps, Overlap{A: i, B: j, Packages: shared})
			}
		}
	}
	return rep, nil
}

// Proto renders the context facts as their wire message.
func ContextProto(seeds []Seed, decls []verify.Decl) *stipulatorv1.ContextReport {
	out := &stipulatorv1.ContextReport{}
	var ss []*stipulatorv1.Seed
	for _, s := range seeds {
		m := &stipulatorv1.Seed{}
		m.SetRequirementId(s.Requirement)
		m.SetBackend(s.Backend)
		m.SetSymbol(s.Symbol)
		m.SetRole(s.Role)
		ss = append(ss, m)
	}
	out.SetSeeds(ss)
	var ds []*stipulatorv1.Decl
	for _, d := range decls {
		m := &stipulatorv1.Decl{}
		m.SetPackage(d.Package)
		m.SetName(d.Name)
		m.SetDeclaration(d.Declaration)
		m.SetShapeHash(d.ShapeHash)
		ds = append(ds, m)
	}
	out.SetDeclarations(ds)
	return out
}

// Proto renders the partition report as its wire message.
func (r *Report) Proto() *stipulatorv1.PartitionReport {
	out := &stipulatorv1.PartitionReport{}
	var comps []*stipulatorv1.PartitionComponent
	for _, c := range r.Components {
		m := &stipulatorv1.PartitionComponent{}
		m.SetRequirementIds(c.Requirements)
		cp := ContextProto(c.Seeds, nil)
		m.SetSeeds(cp.GetSeeds())
		m.SetPackages(c.Packages)
		comps = append(comps, m)
	}
	out.SetComponents(comps)
	var overlaps []*stipulatorv1.PartitionOverlap
	for _, o := range r.Overlaps {
		m := &stipulatorv1.PartitionOverlap{}
		m.SetA(int32(o.A))
		m.SetB(int32(o.B))
		m.SetPackages(o.Packages)
		overlaps = append(overlaps, m)
	}
	out.SetOverlaps(overlaps)
	return out
}

func intersects(a, b map[string]bool) bool {
	if len(b) < len(a) {
		a, b = b, a
	}
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

func sharedStrings(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	var out []string
	for _, s := range b {
		if set[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
