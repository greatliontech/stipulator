package harden

import (
	"errors"
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/bindingsurface"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

type reminderTarget struct {
	Symbol       string
	Requirements []string
	Witnesses    []string
}

type WitnessPin struct {
	Symbol string
	Hash   string
}

// FindingState is why a covered body's finding is not current.
type FindingState string

const (
	// Missing: no recorded finding keys this symbol.
	Missing FindingState = "missing"
	// Stale: a finding exists but its body/witness/toolchain pins no longer
	// match the current tree. Operator-set drift is deliberately not judged
	// here: stipulator cannot know the engine's current operator version,
	// and the engine re-measures on its own bump (REQ-harden-findings).
	Stale FindingState = "stale"
)

// ReminderEntry is one covered implementation body whose finding is not
// current: the symbol, the requirements it covers, why the sheet is not
// current, and whether a body mutator can harden it.
type ReminderEntry struct {
	Symbol       string       `json:"symbol"`
	Requirements []string     `json:"requirements,omitempty"`
	State        FindingState `json:"state"`
	// Hardenable is true when a body mutator can break at least one of the
	// symbol's witnesses — export targets and run the engine. False means no
	// mutation target (witnessed only by analyzer proofs): the staged-delta
	// report explains.
	Hardenable bool `json:"hardenable"`
}

// Reminder is the covered-but-unhardened tail (REQ-harden-coverage-reminder).
type Reminder struct {
	Entries []ReminderEntry
}

// Hardenable returns the entries a body mutator can run — the actionable
// mutation targets.
func (r *Reminder) Hardenable() []ReminderEntry {
	var out []ReminderEntry
	for _, e := range r.Entries {
		if e.Hardenable {
			out = append(out, e)
		}
	}
	return out
}

// Counts returns the number of hardenable entries (run `harden`) and the
// number with no mutation target.
func (r *Reminder) Counts() (hardenable, noTarget int) {
	for _, e := range r.Entries {
		if e.Hardenable {
			hardenable++
		} else {
			noTarget++
		}
	}
	return hardenable, noTarget
}

// ReminderMap is the JSON projection shared by the gate CLI and the MCP gate
// tool, so the two surfaces cannot drift: the entries plus a
// hardenable/no-target roll-up. A nil reminder projects to an empty tail.
func ReminderMap(r *Reminder) map[string]any {
	entries := []ReminderEntry{}
	hardenable, noTarget := 0, 0
	if r != nil {
		if r.Entries != nil {
			entries = r.Entries
		}
		hardenable, noTarget = r.Counts()
	}
	return map[string]any{"entries": entries, "hardenable": hardenable, "noTarget": noTarget}
}

// CoverageReminder lists the covered requirements' implementation bodies with
// no fresh finding: a function bound `implements` that no sheet covers, or
// whose sheet's pins have moved. Non-function bindings have no body to mutate
// and are skipped; a body with a current sheet drops off. toolchain is the
// executing toolchain identity a sheet must match (golang.Toolchain).
// Advisory only — the caller never gates on it (REQ-harden-coverage-reminder).
func CoverageReminder(spec *stipulatorv1.Spec, store *records.Store, backend *golang.Backend, toolchain string, covered []string, findings []EngineFinding) (*Reminder, error) {
	// First match wins on a duplicate symbol; duplicates occur only in
	// hand-edited documents.
	prior := map[string]*EngineFinding{}
	for i := range findings {
		f := &findings[i]
		if _, ok := prior[f.Symbol]; !ok {
			prior[f.Symbol] = f
		}
	}

	coveredSet := make(map[string]bool, len(covered))
	for _, id := range covered {
		coveredSet[id] = true
	}

	rep := &Reminder{}
	targets, err := reminderTargets(spec, store, coveredSet)
	if err != nil {
		return nil, err
	}
	for _, t := range targets {
		bodyHash, err := backend.BodyHash(t.Symbol)
		if errors.Is(err, golang.ErrNotFunction) {
			continue // no body to mutate — never reminded
		}
		if err != nil {
			return nil, err
		}
		witnessPins := make([]WitnessPin, 0, len(t.Witnesses))
		for _, w := range t.Witnesses {
			wh, err := backend.BodyHash(w)
			if err != nil {
				return nil, err
			}
			witnessPins = append(witnessPins, WitnessPin{Symbol: w, Hash: wh})
		}
		rec, has := prior[t.Symbol]
		if has && rec.BodyHash == bodyHash &&
			oraclePinsEqual(rec.Oracle, witnessPins) &&
			rec.Toolchain == toolchain {
			continue // the finding is current — hardened
		}
		state := Missing
		if has {
			state = Stale
		}
		// Freshness and hardenability use the symbol's full witness union
		// (matching the sheet, which pins every implementing requirement's
		// witnesses), but the displayed requirements are the covered subset:
		// the reminder's premise is "each covered requirement".
		rep.Entries = append(rep.Entries, ReminderEntry{
			Symbol:       t.Symbol,
			Requirements: intersect(t.Requirements, coveredSet),
			State:        state,
			Hardenable:   mutatable(backend, t.Witnesses),
		})
	}
	sort.Slice(rep.Entries, func(i, j int) bool { return rep.Entries[i].Symbol < rep.Entries[j].Symbol })
	return rep, nil
}

func reminderTargets(spec *stipulatorv1.Spec, store *records.Store, covered map[string]bool) ([]reminderTarget, error) {
	report, err := bindingsurface.Derive(spec, store)
	if err != nil {
		return nil, err
	}
	var targets []reminderTarget
	for _, surface := range report.GetSurfaces() {
		if surface.GetBackend() != "go" || len(intersect(surface.GetRequirementIds(), covered)) == 0 {
			continue
		}
		target := reminderTarget{Symbol: surface.GetSymbol(), Requirements: surface.GetRequirementIds()}
		witnesses := map[string]bool{}
		for _, binding := range surface.GetBindings() {
			if binding.GetBackend() == "go" {
				witnesses[binding.GetSymbol()] = true
			}
		}
		for witness := range witnesses {
			target.Witnesses = append(target.Witnesses, witness)
		}
		sort.Strings(target.Witnesses)
		targets = append(targets, target)
	}
	return targets, nil
}

func mutatable(backend interface {
	WitnessClass(string) verify.WitnessClass
}, witnesses []string) bool {
	for _, witness := range witnesses {
		if backend.WitnessClass(witness) != verify.AnalyzerProof {
			return true
		}
	}
	return false
}

// intersect returns the ids present in set, preserving ids' order.
func intersect(ids []string, set map[string]bool) []string {
	var out []string
	for _, id := range ids {
		if set[id] {
			out = append(out, id)
		}
	}
	return out
}

// oraclePinsEqual reports whether the finding's oracle pins match the
// current witness pins exactly — same test symbols, each at the same body
// hash — compared as a set, so order never matters. A new or dropped
// witness, or an edit to one, re-stales the finding.
func oraclePinsEqual(recorded []OraclePin, current []WitnessPin) bool {
	if len(recorded) != len(current) {
		return false
	}
	bySym := make(map[string]string, len(recorded))
	for _, p := range recorded {
		bySym[p.Symbol] = p.Hash
	}
	for _, c := range current {
		if h, ok := bySym[c.Symbol]; !ok || h != c.Hash {
			return false
		}
	}
	return true
}
