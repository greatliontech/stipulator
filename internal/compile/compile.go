// Package compile turns a corpus into the IR.
//
// Compilation is a pure function of tree bytes: enumerate the corpus, parse
// each document through the profile extension (which classifies and
// restructures), extract typed blocks, then resolve corpus-wide — identity
// uniqueness, keyword discipline, references, term matching — and assemble
// the canonically-ordered Spec. Diagnostics are the lint channel: a corpus
// with any error-severity diagnostic has no IR; opt-in lint warnings ride
// alongside a clean compile.
package compile

import (
	"bytes"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/canon"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/profile"
	"github.com/greatliontech/stipulator/internal/records"
)

// Diagnostic is a profile violation, or — when Warning is set — an
// opt-in lint observation that surfaces without failing compilation,
// or — when Remedy is set — the operation that renders a violation's
// state, computed once for the faults it accompanies: never a fault
// itself, never counted as one, and never quoted as one by a surface
// that judges a hypothetical corpus (REQ-change-remediation).
type Diagnostic struct {
	Document string
	Line     int
	Message  string
	Warning  bool
	Remedy   bool
}

func (d Diagnostic) String() string {
	switch {
	case d.Warning:
		return fmt.Sprintf("%s:%d: warning: %s", d.Document, d.Line, d.Message)
	case d.Remedy:
		return fmt.Sprintf("%s:%d: remedy: %s", d.Document, d.Line, d.Message)
	}
	return fmt.Sprintf("%s:%d: %s", d.Document, d.Line, d.Message)
}

// Errors filters lint warnings out: the corpus is clean iff no
// error-severity diagnostic remains.
func Errors(diags []Diagnostic) []Diagnostic {
	var out []Diagnostic
	for _, d := range diags {
		if !d.Warning && !d.Remedy {
			out = append(out, d)
		}
	}
	return out
}

// Refusal is the one-line refusal a verb gives on a broken corpus: the
// first error with the remainder counted, followed by every remedy the
// compile computed — so the operation that renders the state reaches
// the reader of the refusal, whichever verb met it
// (REQ-change-remediation). Empty when the corpus compiles.
func Refusal(diags []Diagnostic) string {
	errs := Errors(diags)
	if len(errs) == 0 {
		return ""
	}
	out := errs[0].String()
	if n := len(errs) - 1; n > 0 {
		out += fmt.Sprintf(" (and %d more)", n)
	}
	for _, d := range diags {
		if d.Remedy {
			out += "; remedy: " + d.Message
		}
	}
	return out
}

// Faults is what a surface renders when it refuses on a broken corpus:
// the errors AND the remedies that accompany them, warnings left out —
// so the operation that renders a violation's state reaches every
// reader of the refusal, not only the compile verb's
// (REQ-change-remediation). Empty exactly when Errors is.
func Faults(diags []Diagnostic) []Diagnostic {
	if len(Errors(diags)) == 0 {
		return nil
	}
	var out []Diagnostic
	for _, d := range diags {
		if !d.Warning {
			out = append(out, d)
		}
	}
	return out
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
		root, refLabels, pdiags := profile.ParseDocument(src)
		for _, pd := range pdiags {
			diags = append(diags, Diagnostic{Document: p, Line: pd.Line, Message: pd.Message})
		}
		docs = append(docs, extractDocument(p, root, src, refLabels))
	}

	spec := resolve(docs, tombstones, &diags)
	lintTerms(spec, m.GetTermLint(), &diags)
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
	if len(Errors(diags)) > 0 {
		return nil, diags, nil
	}
	return spec, diags, nil
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
	remedy := func(loc *stipulatorv1.Location, format string, args ...any) {
		*diags = append(*diags, Diagnostic{
			Document: loc.GetDocument(),
			Line:     int(loc.GetLine()),
			Message:  fmt.Sprintf(format, args...),
			Remedy:   true,
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

	// The supersede disposition's unit is the connected component of
	// removed sources and the successors declaring them — a merge names
	// several sources, a split several successors — so the refusal for
	// a dangling supersedes edge names the disposition over the whole
	// component, never a per-edge fragment that would loop (merge) or
	// burn the identity on one successor (split)
	// (REQ-change-split-merge, REQ-change-remediation).
	components := supersedeComponents(docs, reqs, tombstones)
	remedied := map[*supersedeComponent]bool{}

	for _, d := range docs {
		for _, r := range d.reqs {
			if reqs[r.id] != r {
				continue // duplicate, already reported
			}
			from := reqRef(r.id)
			kws := findTokens(r.segs, keywordRe)
			if len(kws) != 1 {
				diag(r.loc, "requirement %s has %d normative keyword occurrences, want exactly 1 — %s (stipulator compile is the lint after every spec edit)", r.id, len(kws), keywordRemedy(len(kws)))
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
							// The mid-disposition corpus — the source
							// already removed, the successor declaring —
							// is exactly the state the supersede
							// disposition consumes: it validates through
							// the tombstone overlay and needs no compiling
							// base, so the refusal names that one step
							// rather than reading as "make it compile
							// first" (REQ-change-split-merge,
							// REQ-change-remediation).
							diag(r.loc, "requirement %s supersedes %s, which is neither declared nor tombstoned", r.id, target)
							// The remedy once per component, at the first
							// dangling edge met in corpus order: every
							// dangling edge of the component names the
							// same one step.
							if c := components[target]; c != nil && !remedied[c] {
								remedied[c] = true
								remedy(r.loc, "if %s removed by this edit, the supersede disposition tombstones and accepts the edges in one step: stipulator dispose supersede --from %s --into %s (mcp: dispose kind=supersede; add --force when no record names a source)", wasOrWere(c.sources), strings.Join(c.sources, ","), strings.Join(c.successors, ","))
							}
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
			// The hash preimage is the canonical text followed by the
			// context extent's blocks, block boundaries preserved
			// (canon.HashParts): consent covers the vocabulary and
			// layout context a reader takes as part of the contract,
			// while the carried text stays the lead+payload alone — and
			// moving words across the lead/extent boundary moves the
			// hash, because that move changes normative status
			// (REQ-model-content-hash, REQ-profile-context-extent).
			ir.SetContentHash(canon.HashParts(extentParts(text, r.extent.segs)...))
			ir.SetSource(r.source)
			// The digest's preimage: the lead with its payload, each
			// extent block, then each of the document's link reference
			// definition labels — the one document-scoped input the
			// canonical text of those blocks depends on — every part
			// digested on its own, so no part can forge a boundary
			// (REQ-model-consent-source).
			ir.SetSourceHash(canon.SourceDigest(append(append([]string{r.source}, r.extent.source...), d.refLabels...)...))
			ir.SetLocation(r.loc)
			// Clauses ride the IR as a refinement of the requirement:
			// ordinal from 1 in payload order, the declared label when
			// the item leads with one, unique per requirement — two
			// items answering to one label would make a clause claim
			// ambiguous (REQ-profile-clauses).
			labels := map[string]int{}
			var clauses []*stipulatorv1.Clause
			for i, c := range r.clauses {
				cl := &stipulatorv1.Clause{}
				cl.SetOrdinal(uint32(i + 1))
				cl.SetText(plainCanon(c.segs))
				if c.label != "" {
					if prev, dup := labels[c.label]; dup {
						diag(c.loc, "requirement %s declares clause label %q twice (clauses %d and %d)", r.id, c.label, prev, i+1)
					}
					labels[c.label] = i + 1
					cl.SetLabel(c.label)
				}
				clauses = append(clauses, cl)
			}
			ir.SetClauses(clauses)
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
			ir.SetContentHash(canon.HashParts(extentParts(text, t.extent.segs)...))
			ir.SetSource(t.source)
			ir.SetLocation(t.loc)
			irTerms = append(irTerms, ir)
		}
		// One walk over the context blocks: the canonical text, source,
		// reference check, orphan-keyword check, and location are one
		// concept for a note and an annotation; only the IR message and
		// the note's attachment differ.
		for _, c := range d.contexts {
			checkOrphan(c.segs, c.loc)
			text := plainCanon(c.segs)
			refs := checkRefs(c.segs, c.loc)
			if !c.note {
				ir := &stipulatorv1.Annotation{}
				ir.SetText(text)
				ir.SetSource(c.source)
				ir.SetReferences(refs)
				ir.SetLocation(c.loc)
				irAnns = append(irAnns, ir)
				continue
			}
			ir := &stipulatorv1.Note{}
			ir.SetText(text)
			ir.SetSource(c.source)
			if c.attached != nil {
				ir.SetAttachedTo(c.attached)
			}
			ir.SetReferences(refs)
			ir.SetLocation(c.loc)
			irNotes = append(irNotes, ir)
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

// plainCanon is the canonical text of a block's segments — the one
// spelling every IR text field derives from.
func plainCanon(segs []profile.Seg) string {
	return canon.Text(profile.Plain(segs))
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
// extentParts assembles an identity's content-hash preimage parts: the
// canonical text followed by the context extent's blocks in document
// order (REQ-model-content-hash) — wider than the carried text exactly
// by the consent-bearing context, with block boundaries preserved by
// canon.HashParts.
func extentParts(text string, extent [][]profile.Seg) []string {
	parts := make([]string, 0, 1+len(extent))
	parts = append(parts, text)
	for _, segs := range extent {
		parts = append(parts, profile.Plain(segs))
	}
	return parts
}

func findTokens(segs []profile.Seg, re *regexp.Regexp) []string {
	var out []string
	for _, run := range detectionRuns(segs) {
		out = append(out, re.FindAllString(run, -1)...)
	}
	return out
}

// termMatcher finds term-name occurrences: case-insensitive, word-boundary,
// longest match winning — a claimed occurrence is blanked so shorter names
// cannot match inside it. Boundaries are rune-level: word characters are
// Unicode letters and digits (an underscore is a boundary), so non-ASCII
// term names match exactly as ASCII ones — Go's regexp \b is ASCII-only
// and silently missed them (REQ-profile-term-matching).
type termMatcher struct {
	names []string // lowercased, longest first
}

func newTermMatcher(terms map[string]*termBlock) *termMatcher {
	m := &termMatcher{}
	for name := range terms {
		m.names = append(m.names, name)
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
		matched := false
		for _, t := range texts {
			for from := 0; from+len(name) <= len(t); {
				i := bytes.Index(t[from:], []byte(name))
				if i < 0 {
					break
				}
				start := from + i
				end := start + len(name)
				if runeBoundaryBefore(t, start) && runeBoundaryAfter(t, end) {
					matched = true
					for k := start; k < end; k++ {
						t[k] = 1 // blank claimed bytes: non-word, keeps boundaries
					}
				}
				from = start + 1
			}
		}
		if matched && name != self {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// isWordRune is the one word-character definition the matcher and the
// term lint share: Unicode letters and digits; an underscore is a
// boundary (REQ-profile-term-matching, REQ-profile-term-lint).
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func runeBoundaryBefore(t []byte, start int) bool {
	if start == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRune(t[:start])
	return !isWordRune(r)
}

func runeBoundaryAfter(t []byte, end int) bool {
	if end == len(t) {
		return true
	}
	r, _ := utf8.DecodeRune(t[end:])
	return !isWordRune(r)
}

// lintTerms emits the opt-in term-name warnings (REQ-profile-term-lint):
// a declared name containing another declared name on word boundaries is
// resolved correctly by longest-match but invisibly at the source; a
// denylist match names a term the corpus decided makes a poor name.
func lintTerms(spec *stipulatorv1.Spec, cfg *stipulatorv1.TermLint, diags *[]Diagnostic) {
	if spec == nil || cfg == nil {
		return
	}
	deny := map[string]bool{}
	for _, w := range cfg.GetDenylist() {
		deny[strings.ToLower(w)] = true
	}
	terms := spec.GetTerms()
	for _, t := range terms {
		name := strings.ToLower(t.GetName())
		loc := t.GetLocation()
		if deny[name] {
			*diags = append(*diags, Diagnostic{
				Document: loc.GetDocument(), Line: int(loc.GetLine()), Warning: true,
				Message: fmt.Sprintf("term %q matches the manifest denylist", t.GetName()),
			})
		}
		if !cfg.GetWarnShadowing() {
			continue
		}
		for _, other := range terms {
			o := strings.ToLower(other.GetName())
			if o == name || !containsWord(name, o) {
				continue
			}
			*diags = append(*diags, Diagnostic{
				Document: loc.GetDocument(), Line: int(loc.GetLine()), Warning: true,
				Message: fmt.Sprintf("term %q contains term %q: occurrences of %q inside %q bind to the longer name (longest match); shadowing is invisible at the source", t.GetName(), other.GetName(), other.GetName(), t.GetName()),
			})
		}
	}
}

// containsWord reports whether hay contains needle on word boundaries -
// the matcher's rune-level definition, so lint warnings stay consistent
// with actual binding behavior (REQ-profile-term-lint).
func containsWord(hay, needle string) bool {
	h := []byte(hay)
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] != needle {
			continue
		}
		if runeBoundaryBefore(h, i) && runeBoundaryAfter(h, i+len(needle)) {
			return true
		}
	}
	return false
}

// keywordRemedy names the edit that satisfies the one-keyword rule for
// the count found: a requirement with several keywords is several
// clauses with several enforcement states (split them, or coordinate
// them under one keyword), one with none is prose that never binds
// (REQ-profile-one-keyword).
func keywordRemedy(count int) string {
	if count == 0 {
		return "state the obligation with one of MUST, MUST NOT, SHOULD, SHOULD NOT, MAY, or demote the paragraph to prose by dropping its lead"
	}
	return "split the clauses into their own requirements, or coordinate them under one keyword"
}

// supersedeComponent is one connected component of the mid-disposition
// graph: the removed sources and the successors declaring them, each
// sorted — the disposition's unit, and the remedy's spelling.
type supersedeComponent struct {
	sources, successors []string
}

// supersedeComponents groups every dangling supersedes target (neither
// declared nor tombstoned) with the successors declaring it, closed
// under "shares a successor" and "shares a source": a successor
// superseding two removed sources merges them, two successors
// superseding one source split it, and chains of either join. Keyed by
// source.
func supersedeComponents(docs []*document, reqs map[string]*reqBlock, tombstones map[string]bool) map[string]*supersedeComponent {
	declares := map[string]map[string]bool{} // successor → dangling sources
	declaredBy := map[string]map[string]bool{}
	for _, d := range docs {
		for _, r := range d.reqs {
			if reqs[r.id] != r {
				continue
			}
			for _, de := range r.edges {
				if edgeKinds[de.Kind] != stipulatorv1.EdgeKind_EDGE_KIND_SUPERSEDES {
					continue
				}
				for _, target := range de.Targets {
					if _, inCorpus := reqs[target]; inCorpus || tombstones[strings.ToLower(target)] {
						continue
					}
					if declares[r.id] == nil {
						declares[r.id] = map[string]bool{}
					}
					declares[r.id][target] = true
					if declaredBy[target] == nil {
						declaredBy[target] = map[string]bool{}
					}
					declaredBy[target][r.id] = true
				}
			}
		}
	}
	out := map[string]*supersedeComponent{}
	for source := range declaredBy {
		if _, done := out[source]; done {
			continue
		}
		sources := map[string]bool{source: true}
		successors := map[string]bool{}
		queue := []string{source}
		for len(queue) > 0 {
			s := queue[0]
			queue = queue[1:]
			for succ := range declaredBy[s] {
				if successors[succ] {
					continue
				}
				successors[succ] = true
				for other := range declares[succ] {
					if !sources[other] {
						sources[other] = true
						queue = append(queue, other)
					}
				}
			}
		}
		c := &supersedeComponent{}
		for s := range sources {
			c.sources = append(c.sources, s)
		}
		for s := range successors {
			c.successors = append(c.successors, s)
		}
		sort.Strings(c.sources)
		sort.Strings(c.successors)
		for s := range sources {
			out[s] = c
		}
	}
	return out
}

// wasOrWere renders a source list for the remedy sentence.
func wasOrWere(sources []string) string {
	if len(sources) == 1 {
		return sources[0] + " was"
	}
	return strings.Join(sources, ", ") + " were"
}
