// Package verify checks the committed records against the compiled corpus.
//
// This is the consistency layer: dangling identities, malformed records,
// and pin staleness. It holds no language backends — symbol resolution and
// witnesses are backend work layered on top — so its verdicts are about
// record/corpus agreement, never about whether claims are true.
package verify

import (
	"fmt"
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
)

// Problem is a record inconsistency; any problem fails verification.
type Problem struct {
	Path    string
	Message string
}

func (p Problem) String() string { return p.Path + ": " + p.Message }

// Report is the outcome of a consistency run.
type Report struct {
	Problems []Problem
	// Pinned counts bindings whose content-hash pin matches the current
	// corpus; Stale counts bindings whose pin is unset or differs.
	Pinned, Stale int
}

// Run checks the store against the compiled spec.
func Run(spec *stipulatorv1.Spec, store *records.Store) *Report {
	hashes := map[string]string{}
	for _, r := range spec.GetRequirements() {
		hashes[r.GetId()] = r.GetContentHash()
	}

	rep := &Report{}
	problem := func(path, format string, args ...any) {
		rep.Problems = append(rep.Problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}

	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			id := b.GetRequirementId()
			h, known := hashes[id]
			switch {
			case id == "":
				problem(bf.Path, "binding without requirement_id")
			case !known:
				problem(bf.Path, "binding names %s, which is not in the corpus", id)
			case b.GetContentHash() == h:
				rep.Pinned++
			default:
				rep.Stale++
			}
			if b.GetBackend() == "" {
				problem(bf.Path, "binding for %s has no backend", id)
			}
			if b.GetSymbol() == "" {
				problem(bf.Path, "binding for %s has no symbol", id)
			}
			if b.GetRole() == stipulatorv1.BindingRole_BINDING_ROLE_UNSPECIFIED {
				problem(bf.Path, "binding for %s has no role", id)
			}
		}
	}

	for _, gf := range store.Gaps {
		id := gf.Gap.GetRequirementId()
		if _, known := hashes[id]; !known {
			problem(gf.Path, "gap names %s, which is not in the corpus", id)
		}
		if gf.Gap.GetReason() == "" {
			problem(gf.Path, "gap for %s has no reason", id)
		}
		// Landing-condition targets are deliberately not resolved here:
		// exists(...) and covered(...) may name requirements the spec does
		// not hold yet — that prospectiveness is their purpose.
		if !gf.Gap.HasLands() {
			problem(gf.Path, "gap for %s has no landing condition", id)
		}
	}

	sort.Slice(rep.Problems, func(i, j int) bool {
		a, b := rep.Problems[i], rep.Problems[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Message < b.Message
	})
	return rep
}
