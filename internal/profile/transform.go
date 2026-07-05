package profile

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	gtext "github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// IDPattern is the requirement identifier grammar.
const IDPattern = `REQ(-[a-z0-9]+)+`

var (
	idRe = regexp.MustCompile(`^` + IDPattern + `$`)
	// leadRe matches the plain-text prefix of a requirement lead:
	// "REQ-… (metadata): ".
	leadRe = regexp.MustCompile(`^(REQ(?:-[a-z0-9]+)+) \(([^)]*)\): ?`)
	// termLeadRe matches "name (term): " after the strong-span check.
	termLeadRe = regexp.MustCompile(`^(.+?) \(term\): ?`)
)

// ClauseKinds are the valid clause kind names.
var ClauseKinds = map[string]bool{
	"behavior": true, "invariant": true, "structural": true, "wire": true,
}

// EdgeClauses are the valid edge clause names.
var EdgeClauses = map[string]bool{
	"refines": true, "depends": true, "supersedes": true,
}

// Diagnostic is a transform-time profile violation.
type Diagnostic struct {
	Line    int
	Message string
}

var diagKey = parser.NewContextKey()

var md = goldmark.New(
	goldmark.WithExtensions(extension.Table),
	goldmark.WithParserOptions(
		parser.WithASTTransformers(util.Prioritized(&transformer{}, 100)),
	),
)

// Parse parses one corpus document through goldmark with the profile
// transformer installed, returning the normalized tree and any transform-
// time diagnostics.
func Parse(src []byte) (gast.Node, []Diagnostic) {
	var diags []Diagnostic
	pc := parser.NewContext()
	pc.Set(diagKey, &diags)
	root := md.Parser().Parse(gtext.NewReader(src), parser.WithContext(pc))
	return root, diags
}

type transformer struct{}

func (t *transformer) Transform(doc *gast.Document, reader gtext.Reader, pc parser.Context) {
	src := reader.Source()
	diags := pc.Get(diagKey).(*[]Diagnostic)
	li := NewLineIndex(src)
	report := func(n gast.Node, format string, args ...any) {
		start, _ := Span(n, src)
		*diags = append(*diags, Diagnostic{Line: li.Line(start), Message: fmt.Sprintf(format, args...)})
	}

	titles := 0
	var lastIdentity gast.Node // last Requirement or Term in the current section

	child := doc.FirstChild()
	for child != nil {
		next := child.NextSibling()
		switch node := child.(type) {
		case *gast.Heading:
			if node.Level == 1 {
				titles++
			}
			lastIdentity = nil
		case *gast.Paragraph:
			if built := t.paragraph(doc, node, src, report); built != nil {
				if _, isReq := built.(*Requirement); isReq {
					next = adoptPayload(doc, built)
				}
				lastIdentity = built
			} else {
				lastIdentity = nil
			}
		case *gast.Blockquote:
			note := &Note{AttachedTo: lastIdentity}
			doc.ReplaceChild(doc, node, note)
			note.AppendChild(note, node)
		default:
			lastIdentity = nil
		}
		child = next
	}

	if titles != 1 {
		*diags = append(*diags, Diagnostic{
			Line:    1,
			Message: fmt.Sprintf("document must contain exactly one level-1 heading, found %d", titles),
		})
	}
}

// paragraph classifies a paragraph, replacing it with a Requirement or Term
// node when it is a lead. It returns the built node, or nil for ordinary
// paragraphs and malformed leads.
func (t *transformer) paragraph(doc *gast.Document, p *gast.Paragraph, src []byte, report func(gast.Node, string, ...any)) gast.Node {
	strong, ok := p.FirstChild().(*gast.Emphasis)
	if !ok || strong.Level != 2 {
		return nil
	}
	strongText := strings.TrimSpace(Plain(InlineSegs(strong, src)))
	full := Plain(InlineSegs(p, src))

	if m := leadRe.FindStringSubmatch(full); m != nil && strongText == m[1] {
		req := &Requirement{ID: m[1]}
		if !parseMetadata(m[2], req, func(f string, a ...any) { report(p, f, a...) }) {
			return nil
		}
		if !stripMarker(p, strong, len(m[0])-len(m[1])) {
			report(p, "requirement %s: cannot strip lead marker", req.ID)
			return nil
		}
		doc.ReplaceChild(doc, p, req)
		req.AppendChild(req, p)
		return req
	}
	if idRe.MatchString(strongText) {
		report(p, "paragraph begins with %q but does not parse as a requirement lead", strongText)
		return nil
	}
	if m := termLeadRe.FindStringSubmatch(full); m != nil && strongText == m[1] {
		term := &Term{Name: m[1]}
		if !stripMarker(p, strong, len(m[0])-len(m[1])) {
			report(p, "term %q: cannot strip lead marker", term.Name)
			return nil
		}
		doc.ReplaceChild(doc, p, term)
		term.AppendChild(term, p)
		return term
	}
	return nil
}

// parseMetadata parses the comma-separated clauses of the metadata
// parenthetical: the clause kind, then optional edge clauses.
func parseMetadata(meta string, req *Requirement, report func(string, ...any)) bool {
	clauses := strings.Split(meta, ",")
	kind := strings.TrimSpace(clauses[0])
	if !ClauseKinds[kind] {
		report("requirement %s: unknown clause kind %q", req.ID, kind)
		return false
	}
	req.ClauseKind = kind
	for _, clause := range clauses[1:] {
		fields := strings.Fields(clause)
		if len(fields) < 2 {
			report("requirement %s: malformed metadata clause %q", req.ID, strings.TrimSpace(clause))
			return false
		}
		if !EdgeClauses[fields[0]] {
			report("requirement %s: unknown edge clause %q", req.ID, fields[0])
			return false
		}
		for _, target := range fields[1:] {
			if !idRe.MatchString(target) {
				report("requirement %s: %s target %q does not match the identifier grammar", req.ID, fields[0], target)
				return false
			}
		}
		req.Edges = append(req.Edges, DeclaredEdge{Kind: fields[0], Targets: fields[1:]})
	}
	return true
}

// stripMarker removes the lead-in from a lead paragraph: the strong span
// and metaLen bytes of following text (the metadata parenthetical, colon,
// and separating spaces). Plain text maps 1:1 onto source bytes here — the
// metadata grammar admits no markup and soft breaks render one byte wide —
// so byte trimming on text segments is exact.
func stripMarker(p *gast.Paragraph, strong *gast.Emphasis, metaLen int) bool {
	p.RemoveChild(p, strong)
	remain := metaLen
	for remain > 0 {
		c := p.FirstChild()
		text, ok := c.(*gast.Text)
		if !ok {
			return false
		}
		n := text.Segment.Len()
		if text.SoftLineBreak() || text.HardLineBreak() {
			n++
		}
		if n <= remain {
			remain -= n
			p.RemoveChild(p, text)
			continue
		}
		text.Segment.Start += remain
		remain = 0
	}
	return true
}

// adoptPayload moves the contiguous run of list and table blocks following
// a requirement into it, returning the first sibling left in place.
func adoptPayload(doc *gast.Document, req gast.Node) gast.Node {
	for {
		next := req.NextSibling()
		switch next.(type) {
		case *gast.List, *east.Table:
			doc.RemoveChild(doc, next)
			req.AppendChild(req, next)
		default:
			return next
		}
	}
}
