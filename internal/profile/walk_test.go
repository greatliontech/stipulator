package profile

import (
	"fmt"
	"strings"
	"testing"

	gast "github.com/yuin/goldmark/ast"
	"pgregory.net/rapid"

	"github.com/greatliontech/stipulator/stipulate"
)

// block is one top-level unit of a generated document.
type block struct {
	kind string // heading, req, term, para, quote, list, table, code, break
	id   int
}

func (b block) markdown() string {
	switch b.kind {
	case "heading":
		return fmt.Sprintf("## Section %d\n", b.id)
	case "req":
		return fmt.Sprintf("**REQ-r%d** (behavior): Requirement %d MUST hold.\n", b.id, b.id)
	case "term":
		return fmt.Sprintf("**term%d** (term): Term %d is a unit.\n", b.id, b.id)
	case "para":
		return fmt.Sprintf("Ordinary prose %d.\n", b.id)
	case "quote":
		return fmt.Sprintf("> Note %d.\n", b.id)
	case "list":
		// Alternating bullet markers keep adjacent lists distinct blocks
		// (CommonMark merges same-marker lists across a blank line).
		if b.id%2 == 0 {
			return fmt.Sprintf("- item %d\n", b.id)
		}
		return fmt.Sprintf("* item %d\n", b.id)
	case "table":
		return fmt.Sprintf("| a%d |\n| --- |\n| b |\n", b.id)
	case "code":
		return fmt.Sprintf("```\ncode %d\n```\n", b.id)
	case "break":
		return "---\n"
	case "html":
		return fmt.Sprintf("<div>block %d</div>\n", b.id)
	}
	panic(b.kind)
}

// expectation is the spec's own answer for one note or annotation block:
// the identity it attaches to (notes only) and the extent it joins.
type expectation struct {
	kind       string
	attachedTo int // lead index or -1
	context    int // lead index or -1
}

// model replays the two rules the spec states over the abstract
// sequence: attachment is immediate adjacency to the last identity in
// the section (REQ-profile-note); extent membership runs from an
// identity's lead and payload to the next lead, heading, or thematic
// break (REQ-profile-context-extent); a list or table run right after a
// requirement lead is its payload, not a block of its own
// (REQ-profile-payload).
func model(blocks []block) []expectation {
	var out []expectation
	identity, adjacent := -1, false
	for i := 0; i < len(blocks); i++ {
		b := blocks[i]
		switch b.kind {
		case "heading", "break":
			identity, adjacent = -1, false
		case "req", "term":
			identity, adjacent = i, true
			if b.kind == "req" {
				for i+1 < len(blocks) && (blocks[i+1].kind == "list" || blocks[i+1].kind == "table") {
					i++
				}
			}
		case "quote":
			e := expectation{kind: "note", context: identity, attachedTo: -1}
			if adjacent {
				e.attachedTo = identity
			}
			out = append(out, e)
		default:
			out = append(out, expectation{kind: "annotation", context: identity, attachedTo: -1})
			adjacent = false
		}
	}
	return out
}

// One walk answers both windows, and attachment lies inside the extent by
// construction: over arbitrary block sequences the profile's Note and
// Annotation nodes agree with the spec's two rules replayed independently,
// and every attached note's context is its attachment
// (REQ-profile-note, REQ-profile-context-extent).
func TestOneWalkAnswersAttachmentAndExtent(t *testing.T) {
	stipulate.Covers(t, "REQ-profile-note", "REQ-profile-context-extent")
	kinds := []string{"heading", "req", "term", "para", "quote", "list", "table", "code", "break", "html"}
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 12).Draw(rt, "blocks")
		blocks := make([]block, n)
		for i := range blocks {
			blocks[i] = block{kind: rapid.SampledFrom(kinds).Draw(rt, fmt.Sprintf("kind%d", i)), id: i}
		}
		var doc strings.Builder
		doc.WriteString("# Doc\n\n")
		for _, b := range blocks {
			doc.WriteString(b.markdown())
			doc.WriteString("\n")
		}
		root, diags := Parse([]byte(doc.String()))
		if len(diags) != 0 {
			rt.Fatalf("diagnostics over %q: %v", doc.String(), diags)
		}
		want := model(blocks)
		// Identities by lead index: the walk's nodes carry the lead text.
		leadIndex := func(n gast.Node) int {
			switch v := n.(type) {
			case nil:
				return -1
			case *Requirement:
				var id int
				fmt.Sscanf(v.ID, "REQ-r%d", &id)
				return id
			case *Term:
				var id int
				fmt.Sscanf(v.Name, "term%d", &id)
				return id
			}
			rt.Fatalf("unexpected identity node %T", n)
			return -2
		}
		var got []expectation
		for c := root.FirstChild(); c != nil; c = c.NextSibling() {
			switch v := c.(type) {
			case *Note:
				got = append(got, expectation{kind: "note", attachedTo: leadIndex(v.AttachedTo()), context: leadIndex(v.Context)})
				if v.Attached && v.Context == nil {
					rt.Fatal("a note attached to no identity")
				}
			case *Annotation:
				got = append(got, expectation{kind: "annotation", attachedTo: -1, context: leadIndex(v.Context)})
			case *Requirement, *Term, *gast.Heading, *gast.ThematicBreak:
			default:
				rt.Fatalf("a top-level %T survived the walk unwrapped", c)
			}
		}
		if len(got) != len(want) {
			rt.Fatalf("over %q: got %d context blocks %v, want %d %v", doc.String(), len(got), got, len(want), want)
		}
		for i := range want {
			if got[i] != want[i] {
				rt.Fatalf("over %q: block %d = %+v, want %+v", doc.String(), i, got[i], want[i])
			}
		}
	})
}

// Named anchors for the attachment window's edges: a note right after a
// requirement's payload still attaches (the payload is the lead's, not a
// block between them), a note after a list that follows a TERM does not
// (a term adopts no payload, so the list is an annotation that closes the
// window) while both notes lie in their identity's extent, and a note
// after a heading attaches to the section and joins no extent
// (REQ-profile-note, REQ-profile-context-extent, REQ-profile-payload).
func TestNoteAttachmentWindowEdges(t *testing.T) {
	stipulate.Covers(t, "REQ-profile-note", "REQ-profile-context-extent")
	src := "# Doc\n\n**REQ-a** (behavior): A MUST hold.\n\n- item\n\n| c |\n| --- |\n| v |\n\n> after payload\n\n**widget** (term): A widget.\n\n- not payload\n\n> after a term's list\n\n## Section\n\n> after a heading\n"
	root, diags := Parse([]byte(src))
	if len(diags) != 0 {
		t.Fatal(diags)
	}
	var notes []*Note
	for c := root.FirstChild(); c != nil; c = c.NextSibling() {
		if n, ok := c.(*Note); ok {
			notes = append(notes, n)
		}
	}
	if len(notes) != 3 {
		t.Fatalf("notes = %d, want 3", len(notes))
	}
	req, isReq := notes[0].Context.(*Requirement)
	if !isReq || req.ID != "REQ-a" || !notes[0].Attached {
		t.Fatalf("note after the payload: context %v attached %v, want attached to REQ-a", notes[0].Context, notes[0].Attached)
	}
	term, isTerm := notes[1].Context.(*Term)
	if !isTerm || term.Name != "widget" || notes[1].Attached {
		t.Fatalf("note after a term's list: context %v attached %v, want widget's extent, unattached", notes[1].Context, notes[1].Attached)
	}
	if notes[2].Context != nil || notes[2].Attached {
		t.Fatalf("note after a heading: context %v attached %v, want neither", notes[2].Context, notes[2].Attached)
	}
}
