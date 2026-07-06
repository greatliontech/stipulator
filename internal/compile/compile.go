// Package compile turns a corpus into the IR.
//
// Compilation is a pure function of tree bytes: enumerate the corpus, parse
// each document through the profile extension (which classifies and
// restructures), extract typed blocks, then resolve corpus-wide — identity
// uniqueness, keyword discipline, references, term matching — and assemble
// the canonically-ordered Spec. Diagnostics are the lint channel: a corpus
// with any diagnostic has no IR.
package compile

import (
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/canon"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/profile"
	"github.com/greatliontech/stipulator/internal/records"
)

// Diagnostic is a profile violation.
type Diagnostic struct {
	Document string
	Line     int
	Message  string
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s:%d: %s", d.Document, d.Line, d.Message)
}

var (
	reqTokenRe = regexp.MustCompile(`\b` + profile.IDPattern + `\b`)
	keywordRe  = regexp.MustCompile(`\b(MUST NOT|MUST|SHOULD NOT|SHOULD|MAY)\b`)
)

var clauseKinds = map[string]stipulatorv1.ClauseKind{
	"behavior":   stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR,
	"invariant":  stipulatorv1.ClauseKind_CLAUSE_KIND_INVARIANT,
	"structural": stipulatorv1.ClauseKind_CLAUSE_KIND_STRUCTURAL,
	"wire":       stipulatorv1.ClauseKind_CLAUSE_KIND_WIRE,
}

var edgeKinds = map[string]stipulatorv1.EdgeKind{
	"refines":    stipulatorv1.EdgeKind_EDGE_KIND_REFINES,
	"depends":    stipulatorv1.EdgeKind_EDGE_KIND_DEPENDS,
	"supersedes": stipulatorv1.EdgeKind_EDGE_KIND_SUPERSEDES,
}

var keywords = map[string]stipulatorv1.Keyword{
	"MUST":       stipulatorv1.Keyword_KEYWORD_MUST,
	"MUST NOT":   stipulatorv1.Keyword_KEYWORD_MUST_NOT,
	"SHOULD":     stipulatorv1.Keyword_KEYWORD_SHOULD,
	"SHOULD NOT": stipulatorv1.Keyword_KEYWORD_SHOULD_NOT,
	"MAY":        stipulatorv1.Keyword_KEYWORD_MAY,
}

// Compile compiles the corpus rooted at fsys. It returns the IR when the
// corpus is clean, or the diagnostics when it is not; err reports
// infrastructure failures (unreadable tree, missing manifest, malformed
// tombstone registry) rather than profile violations.
func Compile(fsys fs.FS) (*stipulatorv1.Spec, []Diagnostic, error) {
	m, err := corpus.LoadManifest(fsys)
	if err != nil {
		return nil, nil, err
	}
	paths, err := corpus.Enumerate(fsys, m)
	if err != nil {
		return nil, nil, err
	}
	retired, err := records.LoadTombstones(fsys)
	if err != nil {
		return nil, nil, err
	}
	tombstones := map[string]bool{}
	for _, r := range retired {
		tombstones[strings.ToLower(r)] = true
	}

	var diags []Diagnostic
	var docs []*document
	for _, p := range paths {
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, nil, fmt.Errorf("reading corpus document: %w", err)
		}
		if !utf8.Valid(src) {
			diags = append(diags, Diagnostic{Document: p, Line: 1, Message: "document is not valid UTF-8"})
			continue
		}
		root, pdiags := profile.Parse(src)
		for _, pd := range pdiags {
			diags = append(diags, Diagnostic{Document: p, Line: pd.Line, Message: pd.Message})
		}
		docs = append(docs, extractDocument(p, root, src))
	}

	spec := resolve(docs, tombstones, &diags)
	sort.Slice(diags, func(i, j int) bool {
		a, b := diags[i], diags[j]
		if a.Document != b.Document {
			return a.Document < b.Document
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Message < b.Message
	})
	if len(diags) > 0 {
		return nil, diags, nil
	}
	return spec, nil, nil
}

// resolve runs corpus-wide checks and assembles the IR.
func resolve(docs []*document, tombstones map[string]bool, diags *[]Diagnostic) *stipulatorv1.Spec {
	diag := func(loc *stipulatorv1.Location, format string, args ...any) {
		*diags = append(*diags, Diagnostic{
			Document: loc.GetDocument(),
			Line:     int(loc.GetLine()),
			Message:  fmt.Sprintf(format, args...),
		})
	}

	// Identity maps and uniqueness.
	reqs := map[string]*reqBlock{}
	terms := map[string]*termBlock{} // key: lowercased name
	for _, d := range docs {
		for _, r := range d.reqs {
			if prev, dup := reqs[r.id]; dup {
				diag(r.loc, "duplicate requirement %s, first declared at %s:%d", r.id, prev.loc.GetDocument(), prev.loc.GetLine())
				continue
			}
			if tombstones[strings.ToLower(r.id)] {
				diag(r.loc, "requirement %s redeclares a tombstoned identity", r.id)
				continue
			}
			reqs[r.id] = r
		}
		for _, t := range d.terms {
			key := strings.ToLower(t.name)
			if prev, dup := terms[key]; dup {
				diag(t.loc, "duplicate term %q, first declared at %s:%d", t.name, prev.loc.GetDocument(), prev.loc.GetLine())
				continue
			}
			if tombstones[key] {
				diag(t.loc, "term %q redeclares a tombstoned identity", t.name)
				continue
			}
			terms[key] = t
		}
	}

	matcher := newTermMatcher(terms)
	edges := map[string]*stipulatorv1.Edge{}
	addEdge := func(from, to *stipulatorv1.NodeRef, kind stipulatorv1.EdgeKind) {
		if refKey(from) == refKey(to) {
			return
		}
		e := &stipulatorv1.Edge{}
		e.SetFrom(from)
		e.SetTo(to)
		e.SetKind(kind)
		edges[fmt.Sprintf("%d|%s|%s", kind, refKey(from), refKey(to))] = e
	}
	// checkRefs validates identifier tokens and returns their NodeRefs.
	checkRefs := func(segs []profile.Seg, loc *stipulatorv1.Location) []*stipulatorv1.NodeRef {
		var out []*stipulatorv1.NodeRef
		seen := map[string]bool{}
		for _, id := range findTokens(segs, reqTokenRe) {
			if seen[id] {
				continue
			}
			seen[id] = true
			if _, ok := reqs[id]; !ok {
				diag(loc, "reference to %s resolves to nothing", id)
				continue
			}
			out = append(out, reqRef(id))
		}
		slices.SortFunc(out, func(a, b *stipulatorv1.NodeRef) int {
			return strings.Compare(refKey(a), refKey(b))
		})
		return out
	}
	checkOrphan := func(segs []profile.Seg, loc *stipulatorv1.Location) {
		for _, kw := range findTokens(segs, keywordRe) {
			diag(loc, "normative keyword %s outside requirement text", kw)
		}
	}

	for _, d := range docs {
		for _, r := range d.reqs {
			if reqs[r.id] != r {
				continue // duplicate, already reported
			}
			from := reqRef(r.id)
			kws := findTokens(r.segs, keywordRe)
			if len(kws) != 1 {
				diag(r.loc, "requirement %s has %d normative keyword occurrences, want exactly 1", r.id, len(kws))
			}
			for _, ref := range checkRefs(r.segs, r.loc) {
				addEdge(from, ref, stipulatorv1.EdgeKind_EDGE_KIND_REFERENCE)
			}
			for _, name := range matcher.match(r.segs, "") {
				addEdge(from, termRef(terms[name].name), stipulatorv1.EdgeKind_EDGE_KIND_USES_TERM)
			}
			for _, de := range r.edges {
				kind := edgeKinds[de.Kind]
				for _, target := range de.Targets {
					_, inCorpus := reqs[target]
					if kind == stipulatorv1.EdgeKind_EDGE_KIND_SUPERSEDES {
						if !inCorpus && !tombstones[strings.ToLower(target)] {
							diag(r.loc, "requirement %s supersedes %s, which is neither declared nor tombstoned", r.id, target)
							continue
						}
					} else if !inCorpus {
						diag(r.loc, "requirement %s %s %s, which resolves to nothing", r.id, de.Kind, target)
						continue
					}
					addEdge(from, reqRef(target), kind)
				}
			}
		}
		for _, t := range d.terms {
			if terms[strings.ToLower(t.name)] != t {
				continue
			}
			from := termRef(t.name)
			checkOrphan(t.segs, t.loc)
			for _, ref := range checkRefs(t.segs, t.loc) {
				addEdge(from, ref, stipulatorv1.EdgeKind_EDGE_KIND_REFERENCE)
			}
			for _, name := range matcher.match(t.segs, strings.ToLower(t.name)) {
				addEdge(from, termRef(terms[name].name), stipulatorv1.EdgeKind_EDGE_KIND_USES_TERM)
			}
		}
		for _, h := range d.headings {
			checkOrphan(h.segs, h.loc)
			for _, id := range findTokens(h.segs, reqTokenRe) {
				if _, ok := reqs[id]; !ok {
					diag(h.loc, "reference to %s resolves to nothing", id)
				}
			}
		}
	}

	spec := &stipulatorv1.Spec{}
	var irDocs []*stipulatorv1.Document
	var irReqs []*stipulatorv1.Requirement
	var irTerms []*stipulatorv1.Term
	var irNotes []*stipulatorv1.Note
	var irAnns []*stipulatorv1.Annotation
	for _, d := range docs {
		doc := &stipulatorv1.Document{}
		doc.SetPath(d.path)
		doc.SetTitle(d.title)
		doc.SetSections(d.sections)
		irDocs = append(irDocs, doc)
		for _, r := range d.reqs {
			if reqs[r.id] != r {
				continue
			}
			text := profile.Plain(r.segs)
			ir := &stipulatorv1.Requirement{}
			ir.SetId(r.id)
			ir.SetKind(clauseKinds[r.kind])
			if kws := findTokens(r.segs, keywordRe); len(kws) == 1 {
				ir.SetKeyword(keywords[kws[0]])
			}
			ir.SetText(canon.Text(text))
			ir.SetContentHash(canon.Hash(text))
			ir.SetSource(r.source)
			ir.SetLocation(r.loc)
			irReqs = append(irReqs, ir)
		}
		for _, t := range d.terms {
			if terms[strings.ToLower(t.name)] != t {
				continue
			}
			text := profile.Plain(t.segs)
			ir := &stipulatorv1.Term{}
			ir.SetName(t.name)
			ir.SetText(canon.Text(text))
			ir.SetContentHash(canon.Hash(text))
			ir.SetSource(t.source)
			ir.SetLocation(t.loc)
			irTerms = append(irTerms, ir)
		}
		for _, n := range d.notes {
			checkOrphan(n.segs, n.loc)
			ir := &stipulatorv1.Note{}
			ir.SetText(canon.Text(profile.Plain(n.segs)))
			ir.SetSource(n.source)
			switch {
			case n.attachedReq != "":
				ir.SetAttachedTo(reqRef(n.attachedReq))
			case n.attachedTerm != "":
				ir.SetAttachedTo(termRef(n.attachedTerm))
			}
			ir.SetReferences(checkRefs(n.segs, n.loc))
			ir.SetLocation(n.loc)
			irNotes = append(irNotes, ir)
		}
		for _, a := range d.anns {
			checkOrphan(a.segs, a.loc)
			ir := &stipulatorv1.Annotation{}
			ir.SetText(canon.Text(profile.Plain(a.segs)))
			ir.SetSource(a.source)
			ir.SetReferences(checkRefs(a.segs, a.loc))
			ir.SetLocation(a.loc)
			irAnns = append(irAnns, ir)
		}
	}

	slices.SortFunc(irDocs, func(a, b *stipulatorv1.Document) int { return strings.Compare(a.GetPath(), b.GetPath()) })
	slices.SortFunc(irReqs, func(a, b *stipulatorv1.Requirement) int { return strings.Compare(a.GetId(), b.GetId()) })
	slices.SortFunc(irTerms, func(a, b *stipulatorv1.Term) int {
		if c := strings.Compare(strings.ToLower(a.GetName()), strings.ToLower(b.GetName())); c != 0 {
			return c
		}
		return strings.Compare(a.GetName(), b.GetName())
	})
	// Identity-less blocks order by content, never by location: location
	// is metadata the layout-independence invariant excludes, so an order
	// derived from it would make the IR depend on how blocks are
	// partitioned into files (REQ-model-layout-independence).
	refKey := func(r *stipulatorv1.NodeRef) string {
		if r.HasTermName() {
			return "t\x00" + r.GetTermName()
		}
		return "r\x00" + r.GetRequirementId()
	}
	slices.SortFunc(irNotes, func(a, b *stipulatorv1.Note) int {
		if c := strings.Compare(refKey(a.GetAttachedTo()), refKey(b.GetAttachedTo())); c != 0 {
			return c
		}
		return strings.Compare(a.GetSource(), b.GetSource())
	})
	slices.SortFunc(irAnns, func(a, b *stipulatorv1.Annotation) int {
		return strings.Compare(a.GetSource(), b.GetSource())
	})

	edgeKeys := make([]string, 0, len(edges))
	for k := range edges {
		edgeKeys = append(edgeKeys, k)
	}
	sort.Strings(edgeKeys)
	irEdges := make([]*stipulatorv1.Edge, 0, len(edges))
	for _, k := range edgeKeys {
		irEdges = append(irEdges, edges[k])
	}

	spec.SetDocuments(irDocs)
	spec.SetRequirements(irReqs)
	spec.SetTerms(irTerms)
	spec.SetNotes(irNotes)
	spec.SetAnnotations(irAnns)
	spec.SetEdges(irEdges)
	return spec
}

func reqRef(id string) *stipulatorv1.NodeRef {
	r := &stipulatorv1.NodeRef{}
	r.SetRequirementId(id)
	return r
}

func termRef(name string) *stipulatorv1.NodeRef {
	r := &stipulatorv1.NodeRef{}
	r.SetTermName(name)
	return r
}

func refKey(r *stipulatorv1.NodeRef) string {
	if r.HasRequirementId() {
		return "0:" + r.GetRequirementId()
	}
	return "1:" + strings.ToLower(r.GetTermName())
}

// detectionRuns merges contiguous non-inert segments: only inert content
// breaks a detection run, so a soft line break can never split a keyword or
// a multi-word term name.
func detectionRuns(segs []profile.Seg) []string {
	var runs []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			runs = append(runs, cur.String())
			cur.Reset()
		}
	}
	for _, s := range segs {
		if s.Inert {
			flush()
			continue
		}
		cur.WriteString(s.Text)
	}
	flush()
	return runs
}

// findTokens runs a regexp over the detection runs, returning matches in
// document order.
func findTokens(segs []profile.Seg, re *regexp.Regexp) []string {
	var out []string
	for _, run := range detectionRuns(segs) {
		out = append(out, re.FindAllString(run, -1)...)
	}
	return out
}

// termMatcher finds term-name occurrences: case-insensitive, word-boundary,
// longest match winning — a claimed occurrence is blanked so shorter names
// cannot match inside it.
type termMatcher struct {
	names []string // lowercased, longest first
	res   map[string]*regexp.Regexp
}

func newTermMatcher(terms map[string]*termBlock) *termMatcher {
	m := &termMatcher{res: map[string]*regexp.Regexp{}}
	for name := range terms {
		m.names = append(m.names, name)
		m.res[name] = regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	}
	sort.Slice(m.names, func(i, j int) bool {
		if len(m.names[i]) != len(m.names[j]) {
			return len(m.names[i]) > len(m.names[j])
		}
		return m.names[i] < m.names[j]
	})
	return m
}

// match returns the lowercased names of terms occurring in the segments,
// sorted; self names its own lowercased identity to skip self-edges.
func (m *termMatcher) match(segs []profile.Seg, self string) []string {
	runs := detectionRuns(segs)
	texts := make([][]byte, 0, len(runs))
	for _, run := range runs {
		texts = append(texts, []byte(strings.ToLower(run)))
	}
	var out []string
	for _, name := range m.names {
		re, matched := m.res[name], false
		for _, t := range texts {
			for _, loc := range re.FindAllIndex(t, -1) {
				matched = true
				for i := loc[0]; i < loc[1]; i++ {
					t[i] = 1 // blank claimed bytes: non-word, keeps boundaries
				}
			}
		}
		if matched && name != self {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
