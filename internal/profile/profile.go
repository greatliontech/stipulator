// Package profile implements the stipulator authoring profile as a goldmark
// extension.
//
// The profile assigns spec-model meaning to native markdown elements; no
// custom syntax exists, so the extension is a pure AST transformer: after
// goldmark parses a document, the transformer classifies paragraphs and
// restructures the tree into typed nodes — a Requirement owns its lead
// paragraph (marker stripped) and its payload blocks as children, a Note
// carries its attachment and its context identity as fields, and every
// other block becomes an Annotation carrying its context identity.
// Downstream consumers walk a normalized tree and never re-derive
// structure: "whose block is this" is answered once, by one walk with one
// reset table, in two windows — the narrow attachment window (immediate
// adjacency, REQ-profile-note) inside the wide extent window (up to the
// next identity lead, heading, or thematic break,
// REQ-profile-context-extent) — so a note's attachment is always an
// identity whose extent it belongs to.
//
// The package is protobuf-free: it maps markdown to typed AST, and the
// compile package maps typed AST to the IR.
package profile

import (
	"fmt"

	gast "github.com/yuin/goldmark/ast"
)

var (
	// KindRequirement identifies Requirement nodes.
	KindRequirement = gast.NewNodeKind("Requirement")
	// KindTerm identifies Term nodes.
	KindTerm = gast.NewNodeKind("Term")
	// KindNote identifies Note nodes.
	KindNote = gast.NewNodeKind("Note")
	// KindAnnotation identifies Annotation nodes.
	KindAnnotation = gast.NewNodeKind("Annotation")
)

// DeclaredEdge is an edge clause from a requirement's metadata
// parenthetical.
type DeclaredEdge struct {
	// Kind is "refines", "depends", or "supersedes".
	Kind string
	// Targets are requirement identifiers, already grammar-checked.
	Targets []string
}

// Requirement is a normative statement. Its children are the lead paragraph
// with the marker (strong span, metadata parenthetical, colon) stripped,
// followed by the payload blocks.
type Requirement struct {
	gast.BaseBlock
	ID string
	// ClauseKind is "behavior", "invariant", "structural", or "wire".
	ClauseKind string
	Edges      []DeclaredEdge
}

// Kind reports the node kind.
func (n *Requirement) Kind() gast.NodeKind { return KindRequirement }

// Dump renders the node for debugging.
func (n *Requirement) Dump(src []byte, level int) {
	gast.DumpHelper(n, src, level, map[string]string{
		"ID": n.ID, "ClauseKind": n.ClauseKind, "Edges": fmt.Sprint(n.Edges),
	}, nil)
}

// Term is a definition; its identity is its name. Its child is the lead
// paragraph with the marker stripped.
type Term struct {
	gast.BaseBlock
	Name string
}

// Kind reports the node kind.
func (n *Term) Kind() gast.NodeKind { return KindTerm }

// Dump renders the node for debugging.
func (n *Term) Dump(src []byte, level int) {
	gast.DumpHelper(n, src, level, map[string]string{"Name": n.Name}, nil)
}

// Note is a non-normative blockquote. Its child is the original blockquote.
// Context is the identity whose context extent the note belongs to — the
// last Requirement or Term since the last heading or thematic break — or
// nil when the note joins no extent (REQ-profile-context-extent); the
// note attaches to Context or to its enclosing section (REQ-profile-note):
// attachment is the extent's adjacency window.
type Note struct {
	contextual
	// Attached reports that the note lies in Context's attachment window
	// — immediately after the identity's lead and payload, blockquotes
	// only — so it attaches to Context; false attaches it to the
	// enclosing section. A note attached to an identity other than its
	// extent's is unrepresentable.
	Attached bool
}

// AttachedTo returns the identity the note attaches to: Context when
// Attached, nil for a section-attached note (REQ-profile-note).
func (n *Note) AttachedTo() gast.Node {
	if n.Attached {
		return n.Context
	}
	return nil
}

// Annotation is any other block — ordinary prose, a list or table outside
// a payload, code, HTML. Its child is the original block; Context is the
// identity whose context extent it belongs to, or nil when it joins none
// (REQ-profile-annotations, REQ-profile-context-extent).
type Annotation struct {
	contextual
}

// contextual is the shape a context block shares: one wrapped child and
// the identity whose context extent it belongs to, or nil.
type contextual struct {
	gast.BaseBlock
	Context gast.Node
}

// Kind reports the node kind.
func (n *Annotation) Kind() gast.NodeKind { return KindAnnotation }

// Dump renders the node for debugging.
func (n *Annotation) Dump(src []byte, level int) {
	gast.DumpHelper(n, src, level, nil, nil)
}

// Kind reports the node kind.
func (n *Note) Kind() gast.NodeKind { return KindNote }

// Dump renders the node for debugging.
func (n *Note) Dump(src []byte, level int) {
	gast.DumpHelper(n, src, level, nil, nil)
}
