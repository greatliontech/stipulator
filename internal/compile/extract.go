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
}

type termBlock struct {
	name   string
	segs   []profile.Seg
	source string
	loc    *stipulatorv1.Location
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
			d.reqs = append(d.reqs, &reqBlock{
				id:     node.ID,
				kind:   node.ClauseKind,
				edges:  node.Edges,
				segs:   profile.BlockSegs(node, src),
				source: profile.Source(node, src),
				loc:    loc(node),
			})
		case *profile.Term:
			d.terms = append(d.terms, &termBlock{
				name:   node.Name,
				segs:   profile.BlockSegs(node, src),
				source: profile.Source(node, src),
				loc:    loc(node),
			})
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
		case *gast.ThematicBreak:
		default:
			d.anns = append(d.anns, &annBlock{
				segs:   profile.BlockSegs(node, src),
				source: profile.Source(node, src),
				loc:    loc(node),
			})
		}
	}
	return d
}
