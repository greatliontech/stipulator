package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/views"
)

type verifyIn struct {
	NoTest bool   `json:"no_test,omitempty"`
	View   string `json:"view,omitempty"`
	Ids    string `json:"ids,omitempty"`
	Filter string `json:"filter,omitempty"`
	Path   string `json:"path,omitempty"`
}

func (s *Server) toolVerify(ctx context.Context, req *mcp.CallToolRequest, in verifyIn) (*mcp.CallToolResult, map[string]any, error) {
	ctx, prog := s.startProgress(ctx, req)
	// The caller's vocabulary is judged before the witness run: a typo
	// refuses before any child process (REQ-check-preparation).
	scope, err := scopeFrom(in.Ids, "", in.Filter, in.Path)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	if err := scope.Validate(); err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	if err := views.ValidateVerifyView(in.View); err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prepared, rep, _, err := s.verifyPass(ctx, in.NoTest, in.Ids)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	spec := prepared.Spec
	m, err := views.VerifyView(rep, views.FactsFrom(spec, rep), in.View, scope)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	return summarized(withStamps(viewLine("verify", m), prog), m)
}
