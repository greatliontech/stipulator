package compile

import (
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
	// extent is the context extent's blocks in document order — the
	// note and annotation blocks following this requirement up to the
	// next identity lead, heading, or thematic break
	// (REQ-profile-context-extent). Consent surface only: it rides the
	// content hash, never the text.
	extent [][]profile.Seg
}

type termBlock struct {
	name   string
	segs   []profile.Seg
	source string
	loc    *stipulatorv1.Location
	// extent as on reqBlock (REQ-profile-context-extent).
	extent [][]profile.Seg
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
	path     string
	title    string
	sections []*stipulatorv1.Section
	reqs     []*reqBlock
	terms    []*termBlock
	notes    []*noteBlock
	anns     []*annBlock
	headings []headingBlock
}

// extractDocument walks a profile-normalized tree into IR building blocks.
// The tree is already classified — this pass only records nodes, section
// paths, and locations.
func extractDocument(path string, root gast.Node, src []byte) *document {
	li := profile.NewLineIndex(src)
	d := &document{path: path}
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

	// openExtent is the open context extent: the identity whose consent
	// surface absorbs following note and annotation blocks until the
	// next identity lead, heading, or thematic break
	// (REQ-profile-context-extent). One pointer, so "two identities
	// open at once" is unrepresentable; nil means no extent is open (a
	// section-attached block joins none).
	var openExtent *[][]profile.Seg
	extend := func(segs []profile.Seg) {
		if openExtent != nil {
			*openExtent = append(*openExtent, segs)
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
			openExtent = nil
		case *profile.Requirement:
			rb := &reqBlock{
				id:     node.ID,
				kind:   node.ClauseKind,
				edges:  node.Edges,
				segs:   profile.BlockSegs(node, src),
				source: profile.Source(node, src),
				loc:    loc(node),
			}
			d.reqs = append(d.reqs, rb)
			openExtent = &rb.extent
		case *profile.Term:
			tb := &termBlock{
				name:   node.Name,
				segs:   profile.BlockSegs(node, src),
				source: profile.Source(node, src),
				loc:    loc(node),
			}
			d.terms = append(d.terms, tb)
			openExtent = &tb.extent
		case *profile.Note:
			nb := &noteBlock{
				segs:   profile.BlockSegs(node, src),
				source: profile.Source(node, src),
				loc:    loc(node),
			}
			switch a := node.AttachedTo.(type) {
			case *profile.Requirement:
				nb.attachedReq = a.ID
			case *profile.Term:
				nb.attachedTerm = a.Name
			}
			d.notes = append(d.notes, nb)
			extend(nb.segs)
		case *gast.ThematicBreak:
			// A thematic break detaches deliberately free-standing
			// context from the preceding identity
			// (REQ-profile-context-extent).
			openExtent = nil
		default:
			ab := &annBlock{
				segs:   profile.BlockSegs(node, src),
				source: profile.Source(node, src),
				loc:    loc(node),
			}
			d.anns = append(d.anns, ab)
			extend(ab.segs)
		}
	}
	return d
}
