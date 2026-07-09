// Package harden is stipulator's side of the mutation seam: it plans the
// mutation surface from the binding store (Plan), exports it as the
// versioned targets document (ExportTargets), reads the engine's findings
// document back (LoadFindings), and reminds about covered bodies without a
// fresh finding (CoverageReminder). Mutation itself happens in the engine —
// the two tools compose through documents, never a shared library, never an
// invocation (REQ-harden-export, REQ-harden-findings).
package harden

import (
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
)

// Target is one implementation symbol paired with the union of its
// killer tests, derived from the bindings of every requirement that
// binds it as implements.
type Target struct {
	Symbol string
	// Requirements name the implementing claims, for reporting only.
	Requirements []string
	// Witnesses are the unioned witness-role test symbols, canonically
	// ordered — the set the resulting finding is pinned to.
	Witnesses []string
}

// WitnessPin is one witness the sheet ran against, pinned by symbol and the
// body hash it ran at: an edit to a bound witness's body moves its hash and
// re-stales the finding, so a strengthened test never leaves a stale survivor.
type WitnessPin struct {
	Symbol string
	Hash   string
}

// Plan derives one target per go implements-symbol: the killer set is
// the union of the witness-role (tests or proves) bindings of every
// requirement binding that symbol as implements. A requirement filter
// keeps symbols implementing at least one selected requirement; a symbol
// filter keeps the named symbols (empty = all). A target with no bound
// witnesses is reported skipped, never silently dropped.
func Plan(spec *stipulatorv1.Spec, store *records.Store, reqs, symbols []string) []Target {
	wantReq := toSet(reqs)
	wantSym := toSet(symbols)
	inCorpus := map[string]bool{}
	for _, r := range spec.GetRequirements() {
		inCorpus[r.GetId()] = true
	}

	implReqs := map[string]map[string]bool{}  // symbol -> implementing requirements
	witnesses := map[string]map[string]bool{} // requirement -> witness test symbols
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if b.GetBackend() != "go" || !inCorpus[b.GetRequirementId()] {
				continue
			}
			id := b.GetRequirementId()
			switch b.GetRole() {
			case stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS:
				if implReqs[b.GetSymbol()] == nil {
					implReqs[b.GetSymbol()] = map[string]bool{}
				}
				implReqs[b.GetSymbol()][id] = true
			case stipulatorv1.BindingRole_BINDING_ROLE_TESTS,
				stipulatorv1.BindingRole_BINDING_ROLE_PROVES:
				if witnesses[id] == nil {
					witnesses[id] = map[string]bool{}
				}
				witnesses[id][b.GetSymbol()] = true
			}
		}
	}

	syms := make([]string, 0, len(implReqs))
	for sym := range implReqs {
		syms = append(syms, sym)
	}
	sort.Strings(syms)

	var out []Target
	for _, sym := range syms {
		if len(wantSym) > 0 && !wantSym[sym] {
			continue
		}
		t := Target{Symbol: sym}
		for id := range implReqs[sym] {
			t.Requirements = append(t.Requirements, id)
		}
		sort.Strings(t.Requirements)
		if len(wantReq) > 0 && !anyIn(t.Requirements, wantReq) {
			continue
		}

		union := map[string]bool{}
		for _, id := range t.Requirements {
			for w := range witnesses[id] {
				union[w] = true
			}
		}
		// The union exports whole — how a witness symbol is grouped or run
		// is the engine's concern, and a symbol the engine cannot resolve
		// surfaces there rather than silently thinning the union here.
		for w := range union {
			t.Witnesses = append(t.Witnesses, w)
		}
		sort.Strings(t.Witnesses)
		out = append(out, t)
	}
	return out
}

func anyIn(items []string, want map[string]bool) bool {
	for _, it := range items {
		if want[it] {
			return true
		}
	}
	return false
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
