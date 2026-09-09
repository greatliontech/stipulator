// Package proptest generates random spec corpora and record stores for
// the property witnesses that quantify core invariants over inputs.
// Generators produce diagnostics-clean corpora by construction: the
// properties quantify over the in-spec input space, so a generated corpus
// that fails to compile is a generator defect, not a counterexample.
//
// A corpus is generated as an ordered pool of layout-free block units;
// partitioning into files and sections is a separate, independently
// random step. Two partitions of one pool are therefore two layouts of
// the same content — the exact quantification the layout-independence
// and location-metadata invariants need. A note travels inside its
// requirement's block unit: attachment is positional, so separating them
// would change content, not layout.
package proptest

import (
	"fmt"
	"strconv"
	"strings"
	"testing/fstest"

	"pgregory.net/rapid"
)

// Corpus is an ordered, layout-free pool of block units plus the
// identities it declares.
type Corpus struct {
	// Blocks are markdown block units in canonical order, without
	// headings; partitioning may only regroup them, never reorder.
	Blocks []string
	// ReqIDs are the declared requirement identifiers, in order.
	ReqIDs []string
	// TermNames are the declared term names.
	TermNames []string
	// Clauses maps each requirement to its payload clauses' labels in
	// ordinal order (an empty label for an unlabeled item), so a
	// generated clause claim can name a clause the requirement
	// declares.
	Clauses map[string][]string
}

// termPool holds prefix-free names so longest-match term resolution never
// depends on which subset is declared.
var termPool = []string{"gadget", "sprocket", "flange", "doohickey"}

var keywords = []string{"MUST", "MUST NOT", "SHOULD", "MAY"}

var kinds = []string{"behavior", "invariant", "wire", "structural"}

// Option narrows the generated corpus space for a property that needs it.
type Option func(*config)

type config struct {
	keywords []string
	kinds    []string
}

// MustOnly restricts requirement keywords to MUST/MUST NOT, so every
// generated requirement demands witness-tier evidence or stronger under
// the default coverage policy.
func MustOnly() Option {
	return func(c *config) { c.keywords = []string{"MUST", "MUST NOT"} }
}

// Kinds restricts the generated clause kinds.
func Kinds(kinds ...string) Option {
	return func(c *config) { c.kinds = kinds }
}

// Gen draws a corpus: 1..8 requirements with optional payloads, notes,
// edges to earlier requirements, and term usage; 0..2 terms; optional
// annotations referencing declared requirements.
func Gen(t *rapid.T, opts ...Option) Corpus {
	cfg := config{keywords: keywords, kinds: kinds}
	for _, o := range opts {
		o(&cfg)
	}
	c := Corpus{Clauses: map[string][]string{}}

	nTerms := rapid.IntRange(0, 2).Draw(t, "terms")
	for i := range nTerms {
		name := termPool[i]
		c.TermNames = append(c.TermNames, name)
		c.Blocks = append(c.Blocks, fmt.Sprintf("**%s** (term): a part named %s.", name, name))
	}

	nReqs := rapid.IntRange(1, 8).Draw(t, "reqs")
	for i := range nReqs {
		id := fmt.Sprintf("REQ-p-r%d", i)
		kw := rapid.SampledFrom(cfg.keywords).Draw(t, "keyword")
		kind := rapid.SampledFrom(cfg.kinds).Draw(t, "kind")

		meta := kind
		if i > 0 && rapid.Bool().Draw(t, "edge") {
			verb := rapid.SampledFrom([]string{"refines", "depends"}).Draw(t, "edgeVerb")
			target := rapid.IntRange(0, i-1).Draw(t, "edgeTarget")
			meta += fmt.Sprintf(", %s REQ-p-r%d", verb, target)
		}

		subject := "It"
		if len(c.TermNames) > 0 && rapid.Bool().Draw(t, "useTerm") {
			subject = "The " + rapid.SampledFrom(c.TermNames).Draw(t, "term")
		}
		// The variant lets distinct requirements collide on text — legal,
		// and a hashing edge worth quantifying over.
		variant := rapid.IntRange(0, 2).Draw(t, "variant")
		var block strings.Builder
		fmt.Fprintf(&block, "**%s** (%s): %s %s hold case %d.", id, meta, subject, kw, variant)

		var labels []string
		for j := range rapid.IntRange(0, 2).Draw(t, "payload") {
			if j == 0 {
				block.WriteString("\n")
			}
			// A labelled item declares a clause label, unique per
			// requirement by its ordinal; an unlabeled item is a clause
			// addressable by ordinal alone (REQ-profile-clauses).
			label := ""
			if rapid.Bool().Draw(t, "labelled") {
				label = fmt.Sprintf("l%d", j)
				fmt.Fprintf(&block, "\n- **%s** item %d", label, rapid.IntRange(0, 3).Draw(t, "item"))
			} else {
				fmt.Fprintf(&block, "\n- item %d", rapid.IntRange(0, 3).Draw(t, "item"))
			}
			labels = append(labels, label)
		}
		c.Clauses[id] = labels
		if rapid.Bool().Draw(t, "note") {
			fmt.Fprintf(&block, "\n\n> Commentary %d.", rapid.IntRange(0, 3).Draw(t, "noteText"))
		}
		c.ReqIDs = append(c.ReqIDs, id)
		c.Blocks = append(c.Blocks, block.String())
	}

	// Several annotations, each distinct in text: annotation order in
	// the IR is keyed by source alone, so a corpus with one could never
	// discriminate an ordering fault. The extent placements glue to the
	// last REQUIREMENT block, captured before any free-standing draw
	// appends a break block after it.
	last := len(c.Blocks) - 1
	for i, n := 0, rapid.IntRange(0, 3).Draw(t, "annotations"); i < n; i++ {
		target := rapid.SampledFrom(c.ReqIDs).Draw(t, fmt.Sprintf("annotationTarget%d", i))
		ann := fmt.Sprintf("See %s for the details (%d).", target, i)
		switch rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("annotationPlacement%d", i)) {
		case 0:
			// An extent member: context travels with its owning identity
			// across every partition (REQ-profile-context-extent — the
			// unit of partition is the identity with its extent), so it
			// glues to the last requirement's block and the property
			// quantifies extent layout-independence.
			c.Blocks[last] += "\n\n" + ann
		case 1:
			// A deeper extent: an intervening note between the
			// requirement and the annotation exercises the extent
			// TRACKER (the walk must keep the extent open across
			// multiple member blocks), not just the hash fold.
			c.Blocks[last] += "\n\n> Bridging commentary.\n\n" + ann
		default:
			// Free-standing context: detached by a thematic break, so no
			// layout can capture it into a preceding identity's extent.
			c.Blocks = append(c.Blocks, "---\n\n"+ann)
		}
	}
	return c
}

// Partition renders the pool as 1..3 files with independently random
// section structure, preserving block order, each file in a randomly
// drawn folder — multi-folder corpora exercise per-directory surfaces
// (readme exclusion, path ordering) that a flat layout leaves untouched.
// The label keeps repeated draws in one test distinct for rapid's
// shrinker.
func (c Corpus) Partition(t *rapid.T, label string) map[string]string {
	nFiles := rapid.IntRange(1, 3).Draw(t, label+"Files")
	// Cut the ordered pool into consecutive runs; empty files are legal.
	cuts := make([]int, nFiles+1)
	cuts[nFiles] = len(c.Blocks)
	for i := 1; i < nFiles; i++ {
		cuts[i] = rapid.IntRange(cuts[i-1], len(c.Blocks)).Draw(t, fmt.Sprintf("%sCut%d", label, i))
	}

	files := map[string]string{}
	for f := range nFiles {
		var b strings.Builder
		fmt.Fprintf(&b, "# Title %c\n", 'A'+f)
		section := 0
		for _, block := range c.Blocks[cuts[f]:cuts[f+1]] {
			if rapid.Bool().Draw(t, label+"Heading") {
				section++
				depth := rapid.IntRange(2, 3).Draw(t, label+"Depth")
				fmt.Fprintf(&b, "\n%s Section %d\n", strings.Repeat("#", depth), section)
			}
			b.WriteString("\n" + block + "\n")
		}
		dir := rapid.SampledFrom([]string{"", "sub/", "sub/deep/"}).Draw(t, label+"Dir")
		files[fmt.Sprintf("specs/%sd%d.md", dir, f)] = b.String()
	}
	return files
}

// FS assembles a corpus filesystem: the manifest, the given spec files,
// and any extra files verbatim.
func FS(files map[string]string, extra map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
	}
	for p, content := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(content)}
	}
	for p, content := range extra {
		fsys[p] = &fstest.MapFile{Data: []byte(content)}
	}
	return fsys
}

// BindingText renders one binding record naming the requirement.
func BindingText(id, contentHash string) string {
	return BindingTextClause(id, contentHash, "", "")
}

// BindingTextPinned renders one binding record with both pins.
func BindingTextPinned(id, contentHash, shapeHash string) string {
	return BindingTextClause(id, contentHash, shapeHash, "")
}

// BindingTextClause renders one binding record, scoped to the clause
// the spelling names (an ordinal's digits or a label; empty claims the
// whole requirement).
func BindingTextClause(id, contentHash, shapeHash, clause string) string {
	return BindingTextPins(id, contentHash, "", shapeHash, clause)
}

// BindingTextPins renders one binding record with every pin the record
// schema carries: content, consent-source, shape, and the clause.
func BindingTextPins(id, contentHash, sourceHash, shapeHash, clause string) string {
	b := "bindings {\n  requirement_id: \"" + id + "\"\n"
	if contentHash != "" {
		b += "  content_hash: \"" + contentHash + "\"\n"
	}
	if sourceHash != "" {
		b += "  source_hash: \"" + sourceHash + "\"\n"
	}
	if shapeHash != "" {
		b += "  shape_hash: \"" + shapeHash + "\"\n"
	}
	b += "  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n"
	if clause != "" {
		if _, err := strconv.Atoi(clause); err == nil {
			b += "  clause_ordinal: " + clause + "\n"
		} else {
			b += "  clause_label: \"" + clause + "\"\n"
		}
	}
	return b + "}\n"
}

// DrawClause draws a clause spelling for a claim on id: the whole
// requirement (empty), or one of its clauses by ordinal or — where the
// item declares one — by label. Every draw resolves against the
// generated corpus, so a property quantifying over valid records stays
// in-spec.
func DrawClause(t *rapid.T, c Corpus, id string) string {
	labels := c.Clauses[id]
	if len(labels) == 0 || rapid.Bool().Draw(t, "wholeClaim") {
		return ""
	}
	i := rapid.IntRange(0, len(labels)-1).Draw(t, "clause")
	if labels[i] != "" && rapid.Bool().Draw(t, "byLabel") {
		return labels[i]
	}
	return strconv.Itoa(i + 1)
}

// GapText renders one gap record naming the requirement; fired marks the
// manual landing condition explicitly fired, the bit gap evaluation
// branches on.
func GapText(id string, fired bool) string {
	if fired {
		return "requirement_id: \"" + id + "\"\nreason: \"generated\"\nlands { manual { condition: \"generated\" fired: true } }\n"
	}
	return "requirement_id: \"" + id + "\"\nreason: \"generated\"\nlands { manual { condition: \"generated\" } }\n"
}

// GapTextMachine renders one gap record with a machine-evaluable landing
// condition — covered(target) or exists(target) — so generators exercise
// the condition arms coverage evaluates without external judgment.
func GapTextMachine(id, target string, exists bool) string {
	kind := "covered"
	if exists {
		kind = "exists"
	}
	return "requirement_id: \"" + id + "\"\nreason: \"generated\"\nlands { " + kind + ": \"" + target + "\" }\n"
}

// AttestationText renders one attestation record naming the requirement.
func AttestationText(id, contentHash string) string {
	s := "attestations {\n  requirement_id: \"" + id + "\"\n"
	if contentHash != "" {
		s += "  content_hash: \"" + contentHash + "\"\n"
	}
	return s + "  reason: \"generated voucher\"\n}\n"
}
