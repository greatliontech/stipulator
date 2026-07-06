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
	var c Corpus

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

		for j := range rapid.IntRange(0, 2).Draw(t, "payload") {
			if j == 0 {
				block.WriteString("\n")
			}
			fmt.Fprintf(&block, "\n- item %d", rapid.IntRange(0, 3).Draw(t, "item"))
		}
		if rapid.Bool().Draw(t, "note") {
			fmt.Fprintf(&block, "\n\n> Commentary %d.", rapid.IntRange(0, 3).Draw(t, "noteText"))
		}
		c.ReqIDs = append(c.ReqIDs, id)
		c.Blocks = append(c.Blocks, block.String())
	}

	if rapid.Bool().Draw(t, "annotation") {
		target := rapid.SampledFrom(c.ReqIDs).Draw(t, "annotationTarget")
		c.Blocks = append(c.Blocks, fmt.Sprintf("See %s for the details.", target))
	}
	return c
}

// Partition renders the pool as 1..3 files with independently random
// section structure, preserving block order. The label keeps repeated
// draws in one test distinct for rapid's shrinker.
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
		files[fmt.Sprintf("specs/d%d.md", f)] = b.String()
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
	return BindingTextPinned(id, contentHash, "")
}

// BindingTextPinned renders one binding record with both pins.
func BindingTextPinned(id, contentHash, shapeHash string) string {
	b := "bindings {\n  requirement_id: \"" + id + "\"\n"
	if contentHash != "" {
		b += "  content_hash: \"" + contentHash + "\"\n"
	}
	if shapeHash != "" {
		b += "  shape_hash: \"" + shapeHash + "\"\n"
	}
	return b + "  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n"
}

// GapText renders one gap record naming the requirement.
func GapText(id string) string {
	return "requirement_id: \"" + id + "\"\nreason: \"generated\"\nlands { attested { condition: \"generated\" } }\n"
}
