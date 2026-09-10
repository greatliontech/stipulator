package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/verify"
)

type retargetIn struct {
	Backend string `json:"backend,omitempty"`
	From    string `json:"from"`
	To      string `json:"to"`
	Check   bool   `json:"check,omitempty"`
}

func (s *Server) toolRetarget(ctx context.Context, req *mcp.CallToolRequest, in retargetIn) (*mcp.CallToolResult, map[string]any, error) {
	backend := in.Backend
	if backend == "" {
		backend = "go"
	}
	ctx, prog := s.startProgress(ctx, req)
	prog.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	backends, err := s.backends(ctx, nil)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	defer verify.CloseBackends(backends)
	res, err := author.Retarget(s.fsys(), backends, backend, in.From, in.To)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	rows, ups := res.Rows, res.Updates
	notes := make([]string, 0, len(rows)+len(res.Pointers))
	for _, r := range rows {
		notes = append(notes, r.Requirement+": "+r.Old+" -> "+r.New)
	}
	for _, p := range res.Pointers {
		notes = append(notes, p.Requirement+": pointer `"+p.Old+"` -> `"+p.New+"` in "+p.Document)
	}
	notes = append(notes, res.Consented...)
	// A rename that moved nothing is an answer with a next step - the
	// prefix mismatches the recorded spelling or the rewrite already
	// landed - never a bare zero.
	if len(rows) == 0 {
		notes = append(notes, fmt.Sprintf("prefix %q matched no bound symbols; verify view=bindings lists the recorded spellings", in.From))
	}
	if in.Check {
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
		out := writeOut{Notes: notes, Check: true}
		return projected(textOnly(fmt.Sprintf("retarget check: %d binding(s)%s would retarget", len(rows), res.PointerClause())), out.proto())
	}
	out, err := s.apply(ups)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	out.Notes = notes
	return projected(textOnly(fmt.Sprintf("retargeted %d binding(s)", len(rows))), out.proto())
}
