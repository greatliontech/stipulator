// Package bundle computes self-contained spec closures for dissemination.
//
// A bundle is the answer to "give an agent everything it needs to read for
// these requirements, and nothing it doesn't": the requested requirements,
// their transitive closure over the typed edges, the definitions of every
// term used, and the notes and annotations attached to the enclosing
// sections. Self-containedness is contract: every identifier and term name
// occurring in the bundle resolves within it.
package bundle

import (
	"fmt"
	"sort"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// Compute returns the bundle for the requested requirement identifiers as
// a filtered Spec: the closure's requirements and terms — expanded to a
// fixed point over the references carried by included notes and
// annotations, so nothing in the bundle dangles — plus the notes and
// annotations attached to the enclosing sections. Unknown identifiers are
// an error.
func Compute(spec *stipulatorv1.Spec, ids []string) (*stipulatorv1.Spec, error) {
	reqs := map[string]*stipulatorv1.Requirement{}
	for _, r := range spec.GetRequirements() {
		reqs[r.GetId()] = r
	}
	terms := map[string]*stipulatorv1.Term{}
	for _, t := range spec.GetTerms() {
		terms[strings.ToLower(t.GetName())] = t
	}

	// Adjacency over closure edges: uses-term, reference, refines, depends.
	// Supersedes is lifecycle, not context.
	adj := map[string][]*stipulatorv1.NodeRef{}
	for _, e := range spec.GetEdges() {
		switch e.GetKind() {
		case stipulatorv1.EdgeKind_EDGE_KIND_USES_TERM,
			stipulatorv1.EdgeKind_EDGE_KIND_REFERENCE,
			stipulatorv1.EdgeKind_EDGE_KIND_REFINES,
			stipulatorv1.EdgeKind_EDGE_KIND_DEPENDS:
			key := refKey(e.GetFrom())
			adj[key] = append(adj[key], e.GetTo())
		}
	}

	inReqs := map[string]bool{}
	inTerms := map[string]bool{}
	var queue []*stipulatorv1.NodeRef
	for _, id := range ids {
		if _, ok := reqs[id]; !ok {
			return nil, fmt.Errorf("requirement %s is not in the corpus", id)
		}
		r := &stipulatorv1.NodeRef{}
		r.SetRequirementId(id)
		queue = append(queue, r)
	}
	// Fixed point: closure over requirement edges, then the references of
	// notes and annotations pulled in by the closure's sections, until no
	// new identity appears.
	for {
		for len(queue) > 0 {
			ref := queue[0]
			queue = queue[1:]
			key := refKey(ref)
			if ref.HasRequirementId() {
				if inReqs[ref.GetRequirementId()] {
					continue
				}
				inReqs[ref.GetRequirementId()] = true
			} else {
				lower := strings.ToLower(ref.GetTermName())
				if inTerms[lower] {
					continue
				}
				inTerms[lower] = true
			}
			queue = append(queue, adj[key]...)
		}
		sections := map[string]bool{}
		for id := range inReqs {
			sections[sectionKey(reqs[id].GetLocation())] = true
		}
		for lower := range inTerms {
			if t, ok := terms[lower]; ok {
				sections[sectionKey(t.GetLocation())] = true
			}
		}
		grew := false
		addRef := func(ref *stipulatorv1.NodeRef) {
			if ref.HasRequirementId() && !inReqs[ref.GetRequirementId()] ||
				ref.HasTermName() && !inTerms[strings.ToLower(ref.GetTermName())] {
				queue = append(queue, ref)
				grew = true
			}
		}
		for _, n := range spec.GetNotes() {
			if sections[sectionKey(n.GetLocation())] {
				for _, ref := range n.GetReferences() {
					addRef(ref)
				}
			}
		}
		for _, a := range spec.GetAnnotations() {
			if sections[sectionKey(a.GetLocation())] {
				for _, ref := range a.GetReferences() {
					addRef(ref)
				}
			}
		}
		if !grew {
			break
		}
	}

	out := &stipulatorv1.Spec{}
	var outReqs []*stipulatorv1.Requirement
	for id := range inReqs {
		outReqs = append(outReqs, reqs[id])
	}
	sort.Slice(outReqs, func(i, j int) bool { return outReqs[i].GetId() < outReqs[j].GetId() })
	out.SetRequirements(outReqs)

	var outTerms []*stipulatorv1.Term
	for lower := range inTerms {
		if t, ok := terms[lower]; ok {
			outTerms = append(outTerms, t)
		}
	}
	sort.Slice(outTerms, func(i, j int) bool {
		return strings.ToLower(outTerms[i].GetName()) < strings.ToLower(outTerms[j].GetName())
	})
	out.SetTerms(outTerms)

	// Notes and annotations attached to the enclosing sections of closure
	// nodes, for context.
	sections := map[string]bool{}
	for _, r := range outReqs {
		sections[sectionKey(r.GetLocation())] = true
	}
	for _, t := range outTerms {
		sections[sectionKey(t.GetLocation())] = true
	}
	var notes []*stipulatorv1.Note
	for _, n := range spec.GetNotes() {
		if sections[sectionKey(n.GetLocation())] {
			notes = append(notes, n)
		}
	}
	out.SetNotes(notes)
	var anns []*stipulatorv1.Annotation
	for _, a := range spec.GetAnnotations() {
		if sections[sectionKey(a.GetLocation())] {
			anns = append(anns, a)
		}
	}
	out.SetAnnotations(anns)

	// Edges internal to the bundle.
	var edges []*stipulatorv1.Edge
	inBundle := func(ref *stipulatorv1.NodeRef) bool {
		if ref.HasRequirementId() {
			return inReqs[ref.GetRequirementId()]
		}
		return inTerms[strings.ToLower(ref.GetTermName())]
	}
	for _, e := range spec.GetEdges() {
		if inBundle(e.GetFrom()) && inBundle(e.GetTo()) {
			edges = append(edges, e)
		}
	}
	out.SetEdges(edges)
	return out, nil
}

// Markdown renders a bundle as a self-contained document for an agent:
// requested requirements first, then the rest of the closure, then terms,
// then contextual notes.
func Markdown(b *stipulatorv1.Spec, requested []string) string {
	var sb strings.Builder
	sb.WriteString("# Spec bundle\n\n")
	sb.WriteString("Self-contained: every identifier and term below resolves within this document.\n")

	req := map[string]bool{}
	for _, id := range requested {
		req[id] = true
	}
	emit := func(r *stipulatorv1.Requirement) {
		fmt.Fprintf(&sb, "\n%s\n", r.GetSource())
		fmt.Fprintf(&sb, "\n> id: %s | kind: %s | keyword: %s | content_hash: %s\n",
			r.GetId(),
			strings.ToLower(strings.TrimPrefix(r.GetKind().String(), "CLAUSE_KIND_")),
			strings.TrimPrefix(r.GetKeyword().String(), "KEYWORD_"),
			r.GetContentHash())
	}
	sb.WriteString("\n## Requested requirements\n")
	for _, r := range b.GetRequirements() {
		if req[r.GetId()] {
			emit(r)
		}
	}
	rest := false
	for _, r := range b.GetRequirements() {
		if !req[r.GetId()] {
			if !rest {
				sb.WriteString("\n## Referenced requirements (context)\n")
				rest = true
			}
			emit(r)
		}
	}
	if len(b.GetTerms()) > 0 {
		sb.WriteString("\n## Terms\n")
		for _, t := range b.GetTerms() {
			fmt.Fprintf(&sb, "\n%s\n", t.GetSource())
		}
	}
	if len(b.GetNotes()) > 0 || len(b.GetAnnotations()) > 0 {
		sb.WriteString("\n## Context notes\n")
		for _, n := range b.GetNotes() {
			fmt.Fprintf(&sb, "\n%s\n", n.GetSource())
		}
		for _, a := range b.GetAnnotations() {
			fmt.Fprintf(&sb, "\n%s\n", a.GetSource())
		}
	}
	return sb.String()
}

func refKey(r *stipulatorv1.NodeRef) string {
	if r.HasRequirementId() {
		return "0:" + r.GetRequirementId()
	}
	return "1:" + strings.ToLower(r.GetTermName())
}

func sectionKey(l *stipulatorv1.Location) string {
	return l.GetDocument() + "\x00" + strings.Join(l.GetSectionPath(), "\x00")
}
