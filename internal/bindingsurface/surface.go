// Package bindingsurface derives backend-independent addresses from binding
// claims. It does not resolve symbols or inspect verification results.
package bindingsurface

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
)

// Format identifies the binding-surface ProtoJSON document contract.
const Format = "stipulator.binding-surfaces/v1"

const domain = "stipulator-binding-surface-v1"

type implementation struct {
	backend string
	symbol  string
}

type associated struct {
	role    stipulatorv1.BindingRole
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
func Derive(spec *stipulatorv1.Spec, store *records.Store) (*stipulatorv1.BindingSurfaceReport, error) {
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
				associatedByRequirement[id][associated{role: role, backend: backend, symbol: symbol}] = true
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

	report := &stipulatorv1.BindingSurfaceReport{}
	report.SetFormat(Format)
	var surfaces []*stipulatorv1.BindingSurface
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

		surface := &stipulatorv1.BindingSurface{}
		surface.SetBackend(key.backend)
		surface.SetSymbol(key.symbol)
		surface.SetRequirementIds(requirements)
		var wireBindings []*stipulatorv1.SurfaceBinding
		for _, binding := range bindings {
			wire := &stipulatorv1.SurfaceBinding{}
			wire.SetRole(binding.role)
			wire.SetBackend(binding.backend)
			wire.SetSymbol(binding.symbol)
			wireBindings = append(wireBindings, wire)
		}
		surface.SetBindings(wireBindings)
		surface.SetId(identifier(key, requirements, bindings))
		surfaces = append(surfaces, surface)
	}
	report.SetSurfaces(surfaces)
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

func roleOrder(role stipulatorv1.BindingRole) int {
	if role == stipulatorv1.BindingRole_BINDING_ROLE_TESTS {
		return 0
	}
	return 1
}

func roleToken(role stipulatorv1.BindingRole) string {
	if role == stipulatorv1.BindingRole_BINDING_ROLE_TESTS {
		return "tests"
	}
	return "proves"
}

func identifier(key implementation, requirements []string, bindings []associated) string {
	sum := sha256.Sum256(canonicalBytes(key, requirements, bindings))
	return hex.EncodeToString(sum[:])
}

func canonicalBytes(key implementation, requirements []string, bindings []associated) []byte {
	var canonical strings.Builder
	writeString(&canonical, domain)
	writeString(&canonical, key.backend)
	writeString(&canonical, key.symbol)
	writeCount(&canonical, len(requirements))
	for _, requirement := range requirements {
		writeString(&canonical, requirement)
	}
	writeCount(&canonical, len(bindings))
	for _, binding := range bindings {
		writeString(&canonical, roleToken(binding.role))
		writeString(&canonical, binding.backend)
		writeString(&canonical, binding.symbol)
	}
	return []byte(canonical.String())
}

func writeString(out *strings.Builder, value string) {
	writeCount(out, len(value))
	out.WriteString(value)
}

func writeCount(out *strings.Builder, count int) {
	out.WriteString(strconv.Itoa(count))
	out.WriteByte(':')
}
