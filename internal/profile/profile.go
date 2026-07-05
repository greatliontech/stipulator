// Package profile implements the stipulator authoring profile as a goldmark
// extension.
//
// The profile assigns spec-model meaning to native markdown elements; no
// custom syntax exists, so the extension is a pure AST transformer: after
// goldmark parses a document, the transformer classifies paragraphs and
// restructures the tree into typed nodes — a Requirement owns its lead
// paragraph (marker stripped) and its payload blocks as children, a Note
// carries its attachment as a field. Downstream consumers walk a normalized
// tree and never re-derive structure.
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

// Note is a non-normative blockquote. Its child is the original blockquote;
// AttachedTo points at the immediately preceding Requirement or Term in the
// same section, or is nil when the note attaches to its enclosing section.
type Note struct {
	gast.BaseBlock
	AttachedTo gast.Node
}

// Kind reports the node kind.
func (n *Note) Kind() gast.NodeKind { return KindNote }

// Dump renders the node for debugging.
func (n *Note) Dump(src []byte, level int) {
	gast.DumpHelper(n, src, level, nil, nil)
}
