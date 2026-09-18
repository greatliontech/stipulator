package mcpserver

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/recordapply"
)

// The write machinery every authoring verb shares.

type writeOut struct {
	recordapply.Result
	Removed int
	// Notes surface non-silent consequences, e.g. a gap's landing
	// condition retarget.
	Notes []string
	// Check marks a preview: nothing was written, the rows say what an
	// apply would do - without it a zero-row check and a zero-write
	// apply are indistinguishable on the wire.
	Check bool
}

// apply lands a batch of record updates through the one record applier
// (REQ-record-cas); on a fault the result names what landed before it,
// for the caller's faulted to carry.
func (s *Server) apply(ups []author.Update) (writeOut, error) {
	landed, err := s.applier.Apply(ups)
	return writeOut{Result: landed}, err
}

// faulted is the tool error over what a faulted operation landed
// before its fault: a tool error carries no typed result, so the files
// that moved ride the text — "nothing written" over a file that moved
// is the misreport REQ-record-cas refuses.
func faulted(out writeOut, err error) error {
	if partial := out.Landed(); partial != "" {
		return fmt.Errorf("%w (%s)", err, partial)
	}
	return err
}

// proto is the write result's wire message: zero counts and an unset
// preview stay absent, as the projection's omitted fields.
func (w writeOut) proto() *stipulatorv1.WriteResult {
	m := &stipulatorv1.WriteResult{}
	m.SetWrote(w.Wrote)
	m.SetDeleted(w.Deleted)
	if w.Removed > 0 {
		m.SetRemoved(int32(w.Removed))
	}
	m.SetNotes(w.Notes)
	if w.Check {
		m.SetCheck(true)
	}
	return m
}

// result is the one-line Content beside the structured writeOut
// (REQ-mcp-response-contract's single payload encoding).
func (w writeOut) result() *mcp.CallToolResult {
	line := fmt.Sprintf("wrote %d, deleted %d", len(w.Wrote), len(w.Deleted))
	if w.Removed > 0 {
		line += fmt.Sprintf(", removed %d claims", w.Removed)
	}
	if len(w.Notes) > 0 {
		line += fmt.Sprintf("; %d notes", len(w.Notes))
	}
	return textOnly(line)
}
