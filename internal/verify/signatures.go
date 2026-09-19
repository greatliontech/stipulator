package verify

import (
	"fmt"
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/remedy"
)

// The change signatures and reason histograms a report derives from its
// results.

// ChangeSignature labels one requirement's change shape.
type ChangeSignature struct {
	RequirementId string
	Label         SignatureLabel
	// Evidence names the observations behind the label, human-readable.
	Evidence []string
}

// SignatureLabel is the change-signature vocabulary.
type SignatureLabel int

const (
	// Rearchitecture: structure moved — a proof-shape pin no longer
	// matches, or a proof failed — while every behavior witness stayed
	// green: the behavior contract is intact under a new shape.
	Rearchitecture SignatureLabel = iota + 1
	// SemanticDrift: a behavior witness failed while the requirement's
	// content pin is current — red with no corresponding spec delta:
	// behavior diverged under a stable contract.
	SemanticDrift
)

// signatures classifies each requirement's change shape from one run's
// binding results, with the record pins as baseline
// (REQ-gate-change-signature). The two labels are disjoint by
// construction: rearchitecture demands every behavior witness green,
// semantic drift demands one red.
func signatures(results []BindingResult) []ChangeSignature {
	type reqState struct {
		proofMoved, proofFailed  []string
		behaviorGreen            int
		redCurrent, redStalePins []string
	}
	states := map[string]*reqState{}
	order := []string{}
	get := func(id string) *reqState {
		st, ok := states[id]
		if !ok {
			st = &reqState{}
			states[id] = st
			order = append(order, id)
		}
		return st
	}
	for _, r := range results {
		st := get(r.RequirementId)
		proof := r.Role == stipulatorv1.BindingRole_BINDING_ROLE_PROVES || r.WitnessClass == AnalyzerProof
		switch {
		case proof && r.Shape == ShapeMismatch:
			st.proofMoved = append(st.proofMoved, r.Symbol)
		case proof && r.TestOutcome == TestFailed:
			st.proofFailed = append(st.proofFailed, r.Symbol)
		case !proof && witnessRole(r.Role):
			switch r.TestOutcome {
			case TestPassed:
				st.behaviorGreen++
			case TestFailed:
				if r.ContentPinned {
					st.redCurrent = append(st.redCurrent, r.Symbol)
				} else {
					st.redStalePins = append(st.redStalePins, r.Symbol)
				}
			}
		}
	}
	var out []ChangeSignature
	sort.Strings(order)
	for _, id := range order {
		st := states[id]
		switch {
		case len(st.redCurrent) > 0:
			var ev []string
			for _, sym := range st.redCurrent {
				ev = append(ev, "behavior witness failed under a current contract: "+sym)
			}
			for _, sym := range st.redStalePins {
				ev = append(ev, "behavior witness failed alongside a spec delta: "+sym)
			}
			out = append(out, ChangeSignature{RequirementId: id, Label: SemanticDrift, Evidence: ev})
		case (len(st.proofMoved) > 0 || len(st.proofFailed) > 0) && len(st.redCurrent)+len(st.redStalePins) == 0 && st.behaviorGreen > 0:
			var ev []string
			for _, sym := range st.proofMoved {
				ev = append(ev, "proof shape moved: "+sym)
			}
			for _, sym := range st.proofFailed {
				ev = append(ev, "proof failed: "+sym)
			}
			ev = append(ev, fmt.Sprintf("behavior green: %d witnesses", st.behaviorGreen))
			ev = append(ev, "shape re-pin available: "+remedy.Pin())
			out = append(out, ChangeSignature{RequirementId: id, Label: Rearchitecture, Evidence: ev})
		}
	}
	return out
}

// ReasonCount is one reason and how many subjects carry it.
type ReasonCount struct {
	Why string
	N   int
}

// ReasonHistogram aggregates per-subject reasons for a human rendering:
// most frequent first, ties broken on the reason text, so identical
// runs render identically (REQ-core-determinism). The per-subject
// attribution stays on the machine result.
func ReasonHistogram(reasons map[string]string) []ReasonCount {
	counts := map[string]int{}
	for _, why := range reasons {
		counts[why]++
	}
	out := make([]ReasonCount, 0, len(counts))
	for why, n := range counts {
		out = append(out, ReasonCount{why, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Why < out[j].Why
	})
	return out
}
