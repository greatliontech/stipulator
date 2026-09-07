package compile

import (
	"fmt"
	"strings"

	gast "github.com/yuin/goldmark/ast"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/profile"
)

type reqBlock struct {
	id     string
	kind   string
	edges  []profile.DeclaredEdge
	segs   []profile.Seg
	source string
	loc    *stipulatorv1.Location
	// extent is the context extent — the note and annotation blocks
	// following this requirement up to the next identity lead, heading,
	// or thematic break (REQ-profile-context-extent). Consent surface
	// only: it rides the content hash and the consent-source digest,
	// never the text.
	extent extent
	// clauses are the payload's top-level list items in order
	// (REQ-profile-clauses); each carries its own location for the
	// duplicate-label diagnostic.
	clauses []clauseBlock
}

type clauseBlock struct {
	label string
	segs  []profile.Seg
	loc   *stipulatorv1.Location
}

type termBlock struct {
	name   string
	segs   []profile.Seg
	source string
	loc    *stipulatorv1.Location
	// extent as on reqBlock (REQ-profile-context-extent).
	extent extent
}

// extent is an identity's context extent: each member block's text
// segments and its raw markdown, in document order, appended together
// so the two can never disagree in length or order.
type extent struct {
	segs   [][]profile.Seg
	source []string
}

type noteBlock struct {
	segs         []profile.Seg
	source       string
	attachedReq  string
	attachedTerm string
	loc          *stipulatorv1.Location
}

type annBlock struct {
	segs   []profile.Seg
	source string
	loc    *stipulatorv1.Location
}

type headingBlock struct {
	segs []profile.Seg
	loc  *stipulatorv1.Location
}

type document struct {
	path string
	// refLabels are the document's link reference definition labels,
	// sorted: the consent-source digest's trailing preimage parts, one
	// per label (REQ-model-consent-source).
	refLabels []string
	title     string
	sections  []*stipulatorv1.Section
	reqs      []*reqBlock
	terms     []*termBlock
	notes     []*noteBlock
	anns      []*annBlock
	headings  []headingBlock
}

// extractDocument walks a profile-normalized tree into IR building blocks.
// The tree is already classified — this pass only records nodes, section
// paths, and locations.
func extractDocument(path string, root gast.Node, src []byte, refLabels []string) *document {
	li := profile.NewLineIndex(src)
	d := &document{path: path, refLabels: refLabels}
	var sectionPath []string
	var sectionLevels []int

	loc := func(n gast.Node) *stipulatorv1.Location {
		start, _ := profile.Span(n, src)
		l := &stipulatorv1.Location{}
		l.SetDocument(path)
		l.SetSectionPath(append([]string(nil), sectionPath...))
		l.SetLine(int32(li.Line(start)))
		return l
	}

	// Which extent a note or annotation joins is the profile walk's
	// answer (Note.Context, Annotation.Context — REQ-profile-context-
	// extent); this pass only maps each identity node to its extent and
	// extends it. No boundary rule lives here.
	extentOf := map[gast.Node]*extent{}
	extend := func(identity gast.Node, segs []profile.Seg, source string) {
		if e := extentOf[identity]; e != nil {
			e.segs = append(e.segs, segs)
			e.source = append(e.source, source)
		}
	}
	for child := root.FirstChild(); child != nil; child = child.NextSibling() {
		switch node := child.(type) {
		case *gast.Heading:
			segs := profile.InlineSegs(node, src)
			heading := strings.TrimSpace(profile.Plain(segs))
			if node.Level == 1 {
				d.title = heading
				sectionPath, sectionLevels = nil, nil
			} else {
				for len(sectionLevels) > 0 && sectionLevels[len(sectionLevels)-1] >= node.Level {
					sectionLevels = sectionLevels[:len(sectionLevels)-1]
					sectionPath = sectionPath[:len(sectionPath)-1]
				}
				sectionLevels = append(sectionLevels, node.Level)
				sectionPath = append(sectionPath, heading)
				s := &stipulatorv1.Section{}
				s.SetHeading(heading)
				s.SetLevel(int32(node.Level))
				start, _ := profile.Span(node, src)
				s.SetLine(int32(li.Line(start)))
				d.sections = append(d.sections, s)
			}
			d.headings = append(d.headings, headingBlock{segs: segs, loc: loc(node)})
		case *profile.Requirement:
			rb := &reqBlock{
				id:     node.ID,
				kind:   node.ClauseKind,
				edges:  node.Edges,
				segs:   profile.BlockSegs(node, src),
				source: profile.Source(node, src),
				loc:    loc(node),
			}
			for _, c := range profile.Clauses(node, src) {
				rb.clauses = append(rb.clauses, clauseBlock{label: c.Label, segs: c.Segs, loc: loc(c.Item)})
			}
			d.reqs = append(d.reqs, rb)
			extentOf[node] = &rb.extent
		case *profile.Term:
			tb := &termBlock{
				name:   node.Name,
				segs:   profile.BlockSegs(node, src),
				source: profile.Source(node, src),
				loc:    loc(node),
			}
			d.terms = append(d.terms, tb)
			extentOf[node] = &tb.extent
		case *profile.Note:
			nb := &noteBlock{
				segs:   profile.BlockSegs(node, src),
				source: profile.Source(node, src),
				loc:    loc(node),
			}
			switch a := node.AttachedTo().(type) {
			case *profile.Requirement:
				nb.attachedReq = a.ID
			case *profile.Term:
				nb.attachedTerm = a.Name
			}
			d.notes = append(d.notes, nb)
			extend(node.Context, nb.segs, nb.source)
		case *profile.Annotation:
			ab := &annBlock{
				segs:   profile.BlockSegs(node, src),
				source: profile.Source(node, src),
				loc:    loc(node),
			}
			d.anns = append(d.anns, ab)
			extend(node.Context, ab.segs, ab.source)
		case *gast.ThematicBreak:
			// Structure only: the profile walk already closed the
			// extent at it, and a break carries no text.
		default:
			// The profile walk wraps every other block; a node reaching
			// here is a profile bug, never a document shape — dropping
			// it silently would lose its text from every check.
			panic(fmt.Sprintf("compile: profile walk left a %T unwrapped", child))
		}
	}
	return d
}
