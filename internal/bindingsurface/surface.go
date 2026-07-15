// Package bindingsurface derives backend-independent addresses from binding
// claims. It does not resolve symbols or inspect verification results.
package bindingsurface

import (
	"fmt"
	"sort"

	surfacewire "github.com/greatliontech/stipulator/bindingsurface"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
)

type implementation struct {
	backend string
	symbol  string
}

type associated struct {
	role    surfacewire.BindingRole
	backend string
	symbol  string
}

type claim struct {
	requirement string
	backend     string
	symbol      string
	role        stipulatorv1.BindingRole
}

// Derive validates the binding graph and projects it into canonical surfaces.
func Derive(spec *stipulatorv1.Spec, store *records.Store) (*surfacewire.Report, error) {
	inCorpus := make(map[string]bool, len(spec.GetRequirements()))
	for _, requirement := range spec.GetRequirements() {
		inCorpus[requirement.GetId()] = true
	}

	implementations := map[implementation]map[string]bool{}
	associatedByRequirement := map[string]map[associated]bool{}
	seen := map[claim]bool{}
	for _, file := range store.Bindings {
		for _, binding := range file.Set.GetBindings() {
			id := binding.GetRequirementId()
			backend := binding.GetBackend()
			symbol := binding.GetSymbol()
			role := binding.GetRole()
			switch {
			case id == "":
				return nil, fmt.Errorf("%s: binding without requirement_id", file.Path)
			case backend == "":
				return nil, fmt.Errorf("%s: binding for %s has no backend", file.Path, id)
			case symbol == "":
				return nil, fmt.Errorf("%s: binding for %s has no symbol", file.Path, id)
			case role != stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS &&
				role != stipulatorv1.BindingRole_BINDING_ROLE_TESTS &&
				role != stipulatorv1.BindingRole_BINDING_ROLE_PROVES:
				return nil, fmt.Errorf("%s: binding for %s has unrecognized role %d", file.Path, id, role)
			case !inCorpus[id]:
				return nil, fmt.Errorf("%s: binding names %s, which is not in the corpus", file.Path, id)
			}

			identity := claim{requirement: id, backend: backend, symbol: symbol, role: role}
			if seen[identity] {
				return nil, fmt.Errorf("%s: duplicate binding: %s %s %s", file.Path, id, symbol, role)
			}
			seen[identity] = true

			switch role {
			case stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS:
				key := implementation{backend: backend, symbol: symbol}
				if implementations[key] == nil {
					implementations[key] = map[string]bool{}
				}
				implementations[key][id] = true
			case stipulatorv1.BindingRole_BINDING_ROLE_TESTS,
				stipulatorv1.BindingRole_BINDING_ROLE_PROVES:
				if associatedByRequirement[id] == nil {
					associatedByRequirement[id] = map[associated]bool{}
				}
				associatedByRequirement[id][associated{
					role: wireRole(role), backend: backend, symbol: symbol,
				}] = true
			}
		}
	}

	keys := make([]implementation, 0, len(implementations))
	for key := range implementations {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].backend != keys[j].backend {
			return keys[i].backend < keys[j].backend
		}
		return keys[i].symbol < keys[j].symbol
	})

	report := &surfacewire.Report{}
	report.SetFormat(surfacewire.Format)
	var surfaces []*surfacewire.Surface
	for _, key := range keys {
		requirements := sortedSet(implementations[key])
		bindingSet := map[associated]bool{}
		for _, id := range requirements {
			for binding := range associatedByRequirement[id] {
				bindingSet[binding] = true
			}
		}
		bindings := make([]associated, 0, len(bindingSet))
		for binding := range bindingSet {
			bindings = append(bindings, binding)
		}
		sort.Slice(bindings, func(i, j int) bool { return lessBinding(bindings[i], bindings[j]) })

		surface := &surfacewire.Surface{}
		surface.SetBackend(key.backend)
		surface.SetSymbol(key.symbol)
		surface.SetRequirementIds(requirements)
		var wireBindings []*surfacewire.Binding
		for _, binding := range bindings {
			wire := &surfacewire.Binding{}
			wire.SetRole(binding.role)
			wire.SetBackend(binding.backend)
			wire.SetSymbol(binding.symbol)
			wireBindings = append(wireBindings, wire)
		}
		surface.SetBindings(wireBindings)
		id, err := surfacewire.Identifier(surface)
		if err != nil {
			return nil, fmt.Errorf("derive binding surface %s %s: %w", key.backend, key.symbol, err)
		}
		surface.SetId(id)
		surfaces = append(surfaces, surface)
	}
	report.SetSurfaces(surfaces)
	if err := surfacewire.Validate(report); err != nil {
		return nil, fmt.Errorf("derive binding surfaces: %w", err)
	}
	return report, nil
}

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func lessBinding(a, b associated) bool {
	if a.role != b.role {
		return roleOrder(a.role) < roleOrder(b.role)
	}
	if a.backend != b.backend {
		return a.backend < b.backend
	}
	return a.symbol < b.symbol
}

func roleOrder(role surfacewire.BindingRole) int {
	if role == surfacewire.BindingRoleTests {
		return 0
	}
	return 1
}

func wireRole(role stipulatorv1.BindingRole) surfacewire.BindingRole {
	if role == stipulatorv1.BindingRole_BINDING_ROLE_TESTS {
		return surfacewire.BindingRoleTests
	}
	return surfacewire.BindingRoleProves
}
