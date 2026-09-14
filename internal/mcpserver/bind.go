package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/recordapply"
	"github.com/greatliontech/stipulator/internal/verify"
)

type bindIn struct {
	Requirement string      `json:"requirement,omitempty"`
	Symbol      string      `json:"symbol,omitempty"`
	Role        string      `json:"role,omitempty"`
	Backend     string      `json:"backend,omitempty"`
	File        string      `json:"file,omitempty"`
	Clause      string      `json:"clause,omitempty"`
	Claims      []bindClaim `json:"claims,omitempty"`
}

type bindClaim struct {
	Requirement string `json:"requirement"`
	Symbol      string `json:"symbol"`
	Role        string `json:"role"`
	Backend     string `json:"backend,omitempty"`
	File        string `json:"file,omitempty"`
	Clause      string `json:"clause,omitempty"`
}

type writeOut struct {
	Wrote   []string
	Deleted []string
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
	return writeOut{Wrote: landed.Wrote, Deleted: landed.Deleted}, err
}

// faulted is the tool error over what a faulted operation landed
// before its fault: a tool error carries no typed result, so the files
// that moved ride the text — "nothing written" over a file that moved
// is the misreport REQ-record-cas refuses.
func faulted(out writeOut, err error) error {
	if partial := (recordapply.Result{Wrote: out.Wrote, Deleted: out.Deleted}).Landed(); partial != "" {
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

func (s *Server) toolBind(ctx context.Context, req *mcp.CallToolRequest, in bindIn) (*mcp.CallToolResult, map[string]any, error) {
	defaultBackend := in.Backend
	if defaultBackend == "" {
		defaultBackend = "go"
	}
	var reqs []author.BindRequest
	switch {
	case len(in.Claims) > 0:
		if in.Requirement != "" || in.Symbol != "" || in.Role != "" || in.File != "" || in.Clause != "" {
			return nil, nil, fmt.Errorf("give either claims or the single-claim fields, not both")
		}
		for _, c := range in.Claims {
			role, err := author.ParseRole(c.Role)
			if err != nil {
				return nil, nil, err
			}
			backendName := c.Backend
			if backendName == "" {
				backendName = defaultBackend
			}
			reqs = append(reqs, author.BindRequest{
				Requirement: c.Requirement, Symbol: c.Symbol, Backend: backendName,
				Role: role, File: c.File, Clause: c.Clause,
			})
		}
	default:
		role, err := author.ParseRole(in.Role)
		if err != nil {
			return nil, nil, err
		}
		reqs = append(reqs, author.BindRequest{
			Requirement: in.Requirement, Symbol: in.Symbol, Backend: defaultBackend,
			Role: role, File: in.File, Clause: in.Clause,
		})
	}
	ctx, prog := s.startProgress(ctx, req)
	prog.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	backends, err := s.backends(ctx, nil)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	defer verify.CloseBackends(backends)
	ups, err := author.Binds(s.fsys(), backends, reqs)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	out, err := s.apply(ups)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, faulted(out, err))
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	return projected(stampedResult(out.result(), prog), out.proto())
}

type unbindIn struct {
	Requirement string `json:"requirement"`
	Symbol      string `json:"symbol,omitempty"`
	Role        string `json:"role,omitempty"`
	Clause      string `json:"clause,omitempty"`
}

func (s *Server) toolUnbind(ctx context.Context, req *mcp.CallToolRequest, in unbindIn) (*mcp.CallToolResult, map[string]any, error) {
	role, err := author.ParseRole(in.Role)
	if err != nil {
		return nil, nil, err
	}
	ups, removed, err := author.Unbind(s.fsys(), in.Requirement, in.Symbol, role, in.Clause)
	if err != nil {
		return nil, nil, err
	}
	out, err := s.apply(ups)
	if err != nil {
		return nil, nil, faulted(out, err)
	}
	out.Removed = removed
	return projected(out.result(), out.proto())
}
