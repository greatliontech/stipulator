package profile

import (
	"bytes"
	"sort"
	"strings"

	gast "github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	gtext "github.com/yuin/goldmark/text"
)

// Seg is a run of plain text extracted from markdown. Inert runs (code
// spans, code blocks, raw HTML, autolink URLs) are part of the text — they
// contribute to canonical text and hashes — but are exempt from keyword,
// reference, and term detection. Detection never crosses Seg boundaries, so
// a Seg edge is always a word boundary.
type Seg struct {
	Text  string
	Inert bool
}

// Plain concatenates segments into the plain-text rendering.
func Plain(segs []Seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

// InlineSegs extracts text segments from a node's inline children.
func InlineSegs(n gast.Node, src []byte) []Seg {
	var out []Seg
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *gast.Text:
			out = append(out, Seg{Text: string(v.Segment.Value(src))})
			if v.SoftLineBreak() || v.HardLineBreak() {
				out = append(out, Seg{Text: " "})
			}
		case *gast.String:
			out = append(out, Seg{Text: string(v.Value)})
		case *gast.CodeSpan:
			var b bytes.Buffer
			for t := c.FirstChild(); t != nil; t = t.NextSibling() {
				if tx, ok := t.(*gast.Text); ok {
					b.Write(tx.Segment.Value(src))
				}
			}
			out = append(out, Seg{Text: b.String(), Inert: true})
		case *gast.AutoLink:
			out = append(out, Seg{Text: string(v.URL(src)), Inert: true})
		case *gast.RawHTML:
			out = append(out, Seg{Text: segmentsText(v.Segments, src), Inert: true})
		default:
			// Emphasis, links, images: recurse into inline children.
			out = append(out, InlineSegs(c, src)...)
		}
	}
	return out
}

// BlockSegs extracts text segments from a block node and its descendants,
// inserting single-space separators between block-level units so distinct
// words never concatenate.
func BlockSegs(n gast.Node, src []byte) []Seg {
	var out []Seg
	switch v := n.(type) {
	case *gast.FencedCodeBlock, *gast.CodeBlock:
		out = append(out, Seg{Text: linesText(n, src), Inert: true})
	case *gast.HTMLBlock:
		t := linesText(n, src)
		if v.HasClosure() {
			t += string(v.ClosureLine.Value(src))
		}
		out = append(out, Seg{Text: t, Inert: true})
	case *gast.ThematicBreak:
	default:
		if hasInlineChildren(n) {
			return InlineSegs(n, src)
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if len(out) > 0 {
				out = append(out, Seg{Text: " "})
			}
			out = append(out, BlockSegs(c, src)...)
		}
	}
	return out
}

func hasInlineChildren(n gast.Node) bool {
	switch n.(type) {
	case *gast.Paragraph, *gast.TextBlock, *gast.Heading, *east.TableCell:
		return true
	}
	return false
}

func linesText(n gast.Node, src []byte) string {
	var b bytes.Buffer
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		s := l.At(i)
		b.Write(s.Value(src))
	}
	return b.String()
}

func segmentsText(segments *gtext.Segments, src []byte) string {
	var b bytes.Buffer
	for i := 0; i < segments.Len(); i++ {
		s := segments.At(i)
		b.Write(s.Value(src))
	}
	return b.String()
}

// LineIndex maps byte offsets to 1-based line numbers.
type LineIndex []int

// NewLineIndex indexes the newlines of src.
func NewLineIndex(src []byte) LineIndex {
	idx := LineIndex{0}
	for i, b := range src {
		if b == '\n' {
			idx = append(idx, i+1)
		}
	}
	return idx
}

// Line returns the 1-based line containing the byte offset.
func (li LineIndex) Line(offset int) int {
	return sort.Search(len(li), func(i int) bool { return li[i] > offset })
}

// Span returns the [start, stop) byte range covered by a block node and its
// descendants, extended left to the start of its first line so list
// markers, blockquote prefixes, and table pipes are included.
func Span(n gast.Node, src []byte) (int, int) {
	start, stop := -1, -1
	var walk func(gast.Node)
	walk = func(m gast.Node) {
		if m.Type() == gast.TypeInline {
			return
		}
		l := m.Lines()
		for i := 0; i < l.Len(); i++ {
			s := l.At(i)
			if start == -1 || s.Start < start {
				start = s.Start
			}
			if s.Stop > stop {
				stop = s.Stop
			}
		}
		for c := m.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	walk(n)
	if start == -1 {
		return 0, 0
	}
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	return start, stop
}

// Source slices the raw markdown covered by a node, trailing newlines
// trimmed.
func Source(n gast.Node, src []byte) string {
	start, stop := Span(n, src)
	return strings.TrimRight(string(src[start:stop]), "\n")
}
