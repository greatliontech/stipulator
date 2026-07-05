// Package diff compares two compiled IRs.
//
// The comparison is per identity — requirement identifiers and term names —
// never per file: a pure reorganization of the same blocks across files
// reports no semantic delta. Obtaining the two IRs (from working trees, git
// revisions, or fixtures) is the caller's concern; the core stays VCS-free.
package diff

import (
	"fmt"
	"sort"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// Report is the per-identity delta between two IRs. MetadataOnly lists are
// location-level changes; everything else is semantic.
type Report struct {
	AddedRequirements        []string
	RemovedRequirements      []string
	TextChangedRequirements  []string
	KindChangedRequirements  []string
	MetadataOnlyRequirements []string

	AddedTerms        []string
	RemovedTerms      []string
	TextChangedTerms  []string
	MetadataOnlyTerms []string

	AddedEdges   []string
	RemovedEdges []string
}

// SemanticallyEmpty reports whether the delta is location metadata only —
// the signature of a pure reorganization.
func (r *Report) SemanticallyEmpty() bool {
	return len(r.AddedRequirements) == 0 && len(r.RemovedRequirements) == 0 &&
		len(r.TextChangedRequirements) == 0 && len(r.KindChangedRequirements) == 0 &&
		len(r.AddedTerms) == 0 && len(r.RemovedTerms) == 0 &&
		len(r.TextChangedTerms) == 0 &&
		len(r.AddedEdges) == 0 && len(r.RemovedEdges) == 0
}

// Lines renders the report deterministically, one change per line.
func (r *Report) Lines() []string {
	var out []string
	emit := func(label string, ids []string) {
		for _, id := range ids {
			out = append(out, label+" "+id)
		}
	}
	emit("added", r.AddedRequirements)
	emit("removed", r.RemovedRequirements)
	emit("text-changed", r.TextChangedRequirements)
	emit("kind-changed", r.KindChangedRequirements)
	emit("metadata-only", r.MetadataOnlyRequirements)
	emit("added term", r.AddedTerms)
	emit("removed term", r.RemovedTerms)
	emit("text-changed term", r.TextChangedTerms)
	emit("metadata-only term", r.MetadataOnlyTerms)
	emit("edge added", r.AddedEdges)
	emit("edge removed", r.RemovedEdges)
	return out
}

// Diff compares two compiled IRs.
func Diff(old, new *stipulatorv1.Spec) *Report {
	r := &Report{}

	oldReqs := reqMap(old)
	newReqs := reqMap(new)
	for id, o := range oldReqs {
		n, ok := newReqs[id]
		if !ok {
			r.RemovedRequirements = append(r.RemovedRequirements, id)
			continue
		}
		// Text and kind are independent axes: the clause kind lives in the
		// marker, outside the content hash, so both can change at once and
		// both must be reported.
		semantic := false
		if o.GetContentHash() != n.GetContentHash() {
			r.TextChangedRequirements = append(r.TextChangedRequirements, id)
			semantic = true
		}
		if o.GetKind() != n.GetKind() {
			r.KindChangedRequirements = append(r.KindChangedRequirements, id)
			semantic = true
		}
		if !semantic && !sameLocation(o.GetLocation(), n.GetLocation()) {
			r.MetadataOnlyRequirements = append(r.MetadataOnlyRequirements, id)
		}
	}
	for id := range newReqs {
		if _, ok := oldReqs[id]; !ok {
			r.AddedRequirements = append(r.AddedRequirements, id)
		}
	}

	oldTerms := termMap(old)
	newTerms := termMap(new)
	for key, o := range oldTerms {
		n, ok := newTerms[key]
		switch {
		case !ok:
			r.RemovedTerms = append(r.RemovedTerms, o.GetName())
		case o.GetContentHash() != n.GetContentHash():
			r.TextChangedTerms = append(r.TextChangedTerms, n.GetName())
		case o.GetName() != n.GetName() || !sameLocation(o.GetLocation(), n.GetLocation()):
			r.MetadataOnlyTerms = append(r.MetadataOnlyTerms, n.GetName())
		}
	}
	for key, n := range newTerms {
		if _, ok := oldTerms[key]; !ok {
			r.AddedTerms = append(r.AddedTerms, n.GetName())
		}
	}

	oldEdges := edgeSet(old)
	newEdges := edgeSet(new)
	for k := range oldEdges {
		if !newEdges[k] {
			r.RemovedEdges = append(r.RemovedEdges, k)
		}
	}
	for k := range newEdges {
		if !oldEdges[k] {
			r.AddedEdges = append(r.AddedEdges, k)
		}
	}

	for _, s := range [][]string{
		r.AddedRequirements, r.RemovedRequirements, r.TextChangedRequirements,
		r.KindChangedRequirements, r.MetadataOnlyRequirements,
		r.AddedTerms, r.RemovedTerms, r.TextChangedTerms, r.MetadataOnlyTerms,
		r.AddedEdges, r.RemovedEdges,
	} {
		sort.Strings(s)
	}
	return r
}

func reqMap(s *stipulatorv1.Spec) map[string]*stipulatorv1.Requirement {
	m := map[string]*stipulatorv1.Requirement{}
	for _, r := range s.GetRequirements() {
		m[r.GetId()] = r
	}
	return m
}

func termMap(s *stipulatorv1.Spec) map[string]*stipulatorv1.Term {
	m := map[string]*stipulatorv1.Term{}
	for _, t := range s.GetTerms() {
		m[strings.ToLower(t.GetName())] = t
	}
	return m
}

func edgeSet(s *stipulatorv1.Spec) map[string]bool {
	m := map[string]bool{}
	for _, e := range s.GetEdges() {
		m[edgeKey(e)] = true
	}
	return m
}

func edgeKey(e *stipulatorv1.Edge) string {
	return fmt.Sprintf("%s %s -> %s",
		strings.TrimPrefix(e.GetKind().String(), "EDGE_KIND_"),
		refString(e.GetFrom()), refString(e.GetTo()))
}

// refString renders an edge endpoint. Term names are case-folded: term
// identity is case-insensitive, so a case-only rename must not read as an
// edge change.
func refString(r *stipulatorv1.NodeRef) string {
	if r.HasRequirementId() {
		return r.GetRequirementId()
	}
	return fmt.Sprintf("%q", strings.ToLower(r.GetTermName()))
}

func sameLocation(a, b *stipulatorv1.Location) bool {
	if a.GetDocument() != b.GetDocument() || a.GetLine() != b.GetLine() {
		return false
	}
	ap, bp := a.GetSectionPath(), b.GetSectionPath()
	if len(ap) != len(bp) {
		return false
	}
	for i := range ap {
		if ap[i] != bp[i] {
			return false
		}
	}
	return true
}
